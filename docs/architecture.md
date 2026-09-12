# Arquitectura y revisión del diagrama

El diagrama de happy path muestra el flujo de dominio, pero dejaba implícitos el ingreso, el egress, la seguridad, las bases de datos y la observabilidad. La arquitectura ejecutable queda así:

```text
Cliente -> Istio ingress -> payment-operator -> participant-payment-manager
                                           \-> payment-router -> Istio egress -> gateway-card / gateway-bank
        cada bounded context posee su PostgreSQL; Envoy -> OTel Collector -> Jaeger
```

## Correcciones

1. El ingress de Istio es el único punto público en Kubernetes. El operador expone `POST/GET /v1/payments`; router y participantes solo exponen rutas internas.
2. El contrato público exige `debtor_participant_id`, `creditor_participant_id`, `amount_minor`, `currency` y `reference`. El operador consulta ambos participantes, deriva el instrumento del deudor y crea un `RouterPaymentRequest` interno.
3. Las pasarelas son mocks externos con contratos distintos: card usa `/v1/card/authorizations` y `AUTHORIZED/DECLINED`; bank usa `/v2/transfers` y `ACCEPTED/REJECTED`. Adapters explícitos normalizan ambos a `SUCCEEDED/FAILED/PENDING`.
4. Hay tres PostgreSQL independientes: órdenes del operador, participantes y estado/idempotencia del router. No se usan colas ni workers.
5. En Compose los mocks sirven TLS autofirmado en puertos 8091/8092. En Kubernetes la aplicación usa HTTP hacia ServiceEntries y el egress gateway hace TLS origination separado por host. Los endpoints apuntan a `host.docker.internal`; esto requiere OrbStack o Docker Desktop con esa resolución.
6. `PeerAuthentication` exige mTLS STRICT. Las `AuthorizationPolicy` son allowlists por workload: ingress→operator, operator→participant/router, cada aplicación→su propio PostgreSQL y router→egress; cualquier workload con una allowlist rechaza lo no incluido. Se mantienen un DENY explícito para `/admin/*` y Kiali es opcional.
7. Istio Telemetry configura muestreo del 100 %. Los servicios conservan métricas Prometheus y propagan `traceparent`, `tracestate` y `x-request-id`. Los mocks crean spans de servidor con el SDK de OpenTelemetry y los exportan por OTLP/HTTP al mismo Collector que el mesh.
8. Participantes almacena roles `debtor` y `creditor`; el operador comprueba el rol de cada parte. La identidad `participant-admin` puede administrar participantes mediante la API; las identidades de pago conservan acceso de lectura.
9. Las pasarelas exigen una credencial de pago que solo recibe el router. Los controles de comportamiento y estadísticas utilizan otra credencial. `mise run setup` genera ambas en `.env`, excluido de Git; `mise run k8s-secrets` crea el Secret del router. Los volúmenes de las pasarelas conservan resultados idempotentes entre reinicios.

## Decisiones y límites

`net/http` y pgx reducen el peso del experimento. El router conserva una reserva `PENDING` antes de contactar al proveedor y serializa solicitudes con la misma clave mediante un bloqueo de sesión de PostgreSQL. Solo anuncia un resultado definitivo después de persistirlo. Un timeout o 5xx queda `PENDING` porque el proveedor pudo haber cobrado; solo un rechazo definitivo queda `FAILED`.

Un cliente puede repetir el POST público con la misma clave y contenido para recuperar un pago incierto. El operador consulta el estado del router y reutiliza su solicitud interna almacenada. El router permite recuperar un `PENDING` después de `PENDING_RECOVERY_AFTER` (30 segundos por defecto), conservando el ID y la clave del proveedor. No hay recuperación automática en segundo plano. La seguridad del reintento depende de que el proveedor conserve su deduplicación; los mocks lo hacen en sus volúmenes.

El breaker en Go se usa en Compose; en Kubernetes se desactiva para medir las reglas de Istio. Los reintentos hacia las pasarelas se configuran en el mesh; Go realiza una llamada por intento de procesamiento. Las pruebas estáticas no certifican los porcentajes y tiempos de la rúbrica: véase [revisión de patrones](revision-patrones.md).
