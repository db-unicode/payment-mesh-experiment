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
7. Istio Telemetry envía el 100% de las trazas al proveedor OTel. Los servicios conservan métricas Prometheus y propagan `traceparent`, `tracestate` y `x-request-id`.

## Decisiones y límites

`net/http` y pgx reducen el peso del experimento. PostgreSQL advisory locks hacen determinista la idempotencia concurrente entre réplicas. Un timeout o 5xx queda `PENDING` porque el proveedor pudo haber cobrado; solo un rechazo definitivo queda `FAILED`. El breaker en Go se usa en Compose; en Kubernetes se desactiva para medir el breaker de Istio. El runner nunca inventa evidencia: cluster sin recursos queda `NOT_EXECUTED`.
