# Grafana para la demostración

Dashboard: [Payment Mesh - Demonstration](http://localhost:3000/d/payment-mesh?from=now-30m&to=now&refresh=10s).

## Arranque reproducible

Con Kubernetes, Istio y las aplicaciones ya desplegados:

```sh
mise run grafana-install
mise run kiali-install
mise run grafana-ui
```

El último comando debe permanecer abierto. Grafana usa un Service ClusterIP; el navegador accede por port-forward de localhost. El acceso anónimo es solo Viewer, la contraseña de administrador se genera en un Secret y no se imprime ni se guarda en Git. No publicar este acceso fuera del laboratorio sin configurar autenticación apropiada.

El dashboard y Prometheus datasource se provisionan desde `deploy/observability/`. Las ediciones de UI no son persistentes: cambiar el JSON versionado y aplicar `kubectl apply -k deploy/observability`. Grafana usa almacenamiento efímero; sus dashboards se reconstruyen desde código al reiniciar. No se cambió el almacenamiento de Prometheus.

## Qué mostrar

| Panel | Demostración / límite |
| --- | --- |
| Payment HTTP outcomes | Separa HTTP 200, 202, 422 y 5xx en las respuestas del operador a ingress. Un 202 no es un pago completado; el agregado puede incluir otros endpoints HTTP del operador invocados por ingress. |
| Payment traffic by destination | Observa el flujo ingress→operador y operador→servicios, excluyendo scrapes de Prometheus. |
| Operator and internal p95 latency | Latencia del servidor operador y de cada salto interno; no equivale al p95 completo del cliente Locust. |
| mTLS coverage | Porcentaje medido por el receptor para los dos saltos internos. Exigir ambas series con tráfico; no-data no equivale a 100%. |
| Per-hop request rate | Compara llamadas a router y participantes; validar dos participantes puede producir dos llamadas por pago. |
| Database TCP connectivity | Apertura de conexiones TCP hacia cada DB, no prueba integridad de datos ni readiness SQL. |
| App targets UP | Esperado 3: operador, router y participantes. |
| Database proxy scrape health | Esperado 1 en las tres bases; indica disponibilidad del scrape del proxy, no la ejecución de consultas SQL. |
| Envoy enforced outlier ejections | Incrementos de expulsiones realmente aplicadas; puede quedar en cero sin fallos inyectados. No-data significa que falta evidencia. |
| Idempotency evidence | Indica los JSON antes/después necesarios para demostrar delta de cobros=1. RPS no demuestra cobros únicos. |

Ventana predeterminada 30m y refresco 10s. Las tasas usan ventanas de 5m, por lo que se suavizan respecto a los picos de Locust. Los paneles con el mismo porcentaje pueden tener líneas superpuestas. Jaeger y Kiali están enlazados desde la cabecera.

Validación realizada: Grafana 12.4.0 saludable, datasource Prometheus OK, Kiali identifica su versión y URL. Una carga nueva de 45s produjo 1.529 pagos, cero fallos, p95 de Locust 33ms. Los paneles se inspeccionaron visualmente con esa carga; no se repitieron los seis experimentos.

## Correcciones de las alertas de Kiali

- Grafana pasó de una integración apuntando a un servicio inexistente a un deployment real, con URLs interna/externa explícitas.
- `payment-egress-mtls` selecciona el egress real con `istio: egressgateway` en ambos subsets; conserva los SNI separados de card y bank.
- `allow-participant-admin` usa el matcher exacto `serviceAccounts: [payments/participant-admin]`. La identidad administrativa se usa en Jobs transitorios, no necesita un pod permanente. Se mantiene mTLS STRICT y el permiso se limita a POST `/v1/participants`.
- El Job `participant-admin-check` envía `{}` y exige HTTP400: demuestra que Istio autoriza y la aplicación rechaza el cuerpo antes de escribir datos. También se verificó una copia con SA `payment-operator` que exige HTTP403. No se amplió acceso al namespace ni se reemplazó la identidad administrativa por la del operador.
- La NetworkPolicy `kiali-prometheus-telemetry` permite solo a Prometheus del mismo namespace leer 15020 de Kiali. El resto de su política de red se mantiene.
- El namespace raíz de Istio en Kiali se corrigió a `istio-system`.

Las notificaciones antiguas de la pestaña pueden permanecer hasta recargar. No se desactivaron validaciones para ocultarlas. Se debe distinguir la bandeja histórica del resultado actual de Istio Config.

Referencias: [Provisioning de Grafana](https://grafana.com/docs/grafana/latest/administration/provisioning/), [integración Kiali/Grafana](https://kiali.io/docs/configuration/p8s-jaeger-grafana/grafana/), [AuthorizationPolicy de Istio](https://istio.io/latest/docs/reference/config/security/authorization-policy/).
