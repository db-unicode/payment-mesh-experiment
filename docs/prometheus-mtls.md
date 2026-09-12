# Verificación de Prometheus y mTLS

Verificado el 11 de septiembre de 2026, 23:21 America/Bogota (12 de septiembre UTC).

## Causa y solución desplegada

El sidecar de Prometheus interceptaba los scrapes a las IP de los pods en puertos de telemetría. Los targets de Istio devolvían EOF/reset y no se recogían los contadores del receptor. Las series del propio Prometheus no demostraban la seguridad del tráfico de pagos. Además, el VirtualService del router no tenía ruta para `GET /metrics` y su job recibía HTTP 404.

- Se excluyeron de la interceptación saliente de Prometheus únicamente los puertos `15020,15090,15014`, en `deploy/observability/prometheus.yaml`.
- Se añadió la ruta exacta `GET /metrics` del router en `deploy/k8s/base/istio.yaml`.
- Se aplicaron ambos manifiestos y se reinició Prometheus. El puerto de aplicación 8080 sigue pasando por Istio y se conserva mTLS STRICT.

El scraping directo de los endpoints de telemetría concuerda con la [integración oficial de Istio con Prometheus](https://istio.io/latest/docs/ops/integrations/prometheus/). No se desactivó mTLS para solucionar la observabilidad.

## Resultado con tráfico nuevo

Carga happy de 30 segundos, 5 usuarios: **1.029 pagos, cero fallos, p95 28 ms**. Diferencia de contadores del receptor antes/después, sin reiniciar los proxies entre muestras:

| Salto interno | Solicitudes nuevas | mutual_tls |
| --- | ---: | ---: |
| payment-operator → payment-router | 1.029 | 1.029 |
| payment-operator → participant-payment-manager | 2.058 | 2.058 |
| Total | 3.087 | 100% |

No aparecieron incrementos con otra política de seguridad. Se usan exclusivamente series `reporter="destination"` y `source_workload="payment-operator"`, no el tráfico de scraping de Prometheus.

Todos los targets de pagos y los tres jobs de aplicación estaban UP. Hay un target separado de Kiali con conexión rechazada en 15020; no participa en esta medición.

Evidencia cruda: `evidence/raw/mtls-verification/targets-before.json`, `targets-after.json`, `counters-before.json`, `counters-after.json` y `locust_stats.csv`.

Consulta de contadores utilizada:

```promql
sum by (source_workload, destination_workload, connection_security_policy) (
  istio_requests_total{
    reporter="destination",
    source_workload="payment-operator",
    destination_workload_namespace="payments",
    destination_workload=~"payment-router|participant-payment-manager"
  }
)
```

Esta verificación es posterior a los seis experimentos de `20260912T040738Z`: no certifica retrospectivamente un porcentaje de mTLS para aquel run ni representa una repetición de los seis experimentos.

El runner captura ahora `metrics/istio-mtls.json` mediante `increase` en la ventana de carga con margen de scraping. Esa función de Prometheus extrapola y puede producir cantidades fraccionarias: sirve para el porcentaje de seguridad, no para reemplazar el conteo exacto de pagos de Locust. La captura se comprobó contra esta misma ventana y devolvió ambos saltos exclusivamente con `mutual_tls`. El informe exige tráfico positivo en ambos saltos; la configuración por sí sola o una respuesta vacía no demuestran el 100%.
