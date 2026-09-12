# Informe del experimento Payment Mesh

Generado: `2026-09-12T04:15:26Z`

## Objetivo y método

Se evalúan seis hipótesis sobre bounded contexts, zero-trust, resiliencia, normalización multi-proveedor, idempotencia y aislamiento de base de datos. La corrida conserva Locust CSV/HTML, métricas, trazas, contadores, eventos y logs. El resultado se calcula únicamente a partir de esos artefactos.

Ejecución más reciente: `evidence/runs/20260912T040738Z`.

## Resumen medido

| Experimento | Métrica observada | Valor medido | Criterio | Resultado | Evidencia |
|---|---|---|---|---|---|
| `01-happy-mesh` | Locust solicitudes, fallos, trazas completas y mTLS | solicitudes=8366; fallos=0; p95=20 ms; trazas=20; trazas completas=20; mTLS=config-only | 0 fallos, traza completa operator→participant-manager→router→gateway y mTLS activo (configuración; métrica de tráfico si fue capturada) | **PASS** | [evidence/runs/20260912T040738Z/01-happy-mesh](../evidence/runs/20260912T040738Z/01-happy-mesh) |
| `02-instance-failure` | Éxito HTTP 200 en toda la corrida y recuperación de tráfico medida | HTTP 200 success=99.99%; fallos=1; p95=31 ms; pod-ready=0s; traffic recovery=0s | >=99% HTTP 200 en toda la corrida; una sonda de pago HTTP 200/SUCCEEDED se recupera en <30s (readiness se reporta aparte) | **PASS** | [evidence/runs/20260912T040738Z/02-instance-failure](../evidence/runs/20260912T040738Z/02-instance-failure) |
| `03-gateway-degraded` | Locust fallos, p95, timeout, estado y breaker | fallos=0; p95=79 ms; HTTP202=true; PENDING=true; ejections=0→3 | 0 fallos, HTTP 202, PENDING y aumenta el contador de expulsiones Envoy | **PASS** | [evidence/runs/20260912T040738Z/03-gateway-degraded](../evidence/runs/20260912T040738Z/03-gateway-degraded) |
| `04-provider-normalization` | Estados observados | SUCCEEDED, FAILED, SUCCEEDED, FAILED | card/bank exitoso→SUCCEEDED; decline/reject→FAILED | **PASS** | [evidence/runs/20260912T040738Z/04-provider-normalization](../evidence/runs/20260912T040738Z/04-provider-normalization) |
| `05-idempotency-duplicates` | Δ cobros efectivos, respuestas y checksum | delta=1; respuestas=10; checksum=true | delta=1, 10 respuestas y checksum idéntico | **PASS** | [evidence/runs/20260912T040738Z/05-idempotency-duplicates](../evidence/runs/20260912T040738Z/05-idempotency-duplicates) |
| `06-participant-db-failure` | HTTP y readiness de router DB | HTTP=503; accepting connections=true | HTTP 503 y router DB lista | **PASS** | [evidence/runs/20260912T040738Z/06-participant-db-failure](../evidence/runs/20260912T040738Z/06-participant-db-failure) |

## Metadatos y evidencia

| Experimento | Hipótesis | Perfil | Estado del runner | Evidencia |
|---|---|---|---|---|
| `01-happy-mesh` | Una orden válida atraviesa ingress, mTLS y tracing. | 10 usuarios / 2 usuarios/s / 120s | `EXECUTED` | [evidence/runs/20260912T040738Z/01-happy-mesh](../evidence/runs/20260912T040738Z/01-happy-mesh) |
| `02-instance-failure` | Eliminar una réplica no interrumpe el servicio. | 10 usuarios / 2 usuarios/s / 120s | `FAILED` | [evidence/runs/20260912T040738Z/02-instance-failure](../evidence/runs/20260912T040738Z/02-instance-failure) |
| `03-gateway-degraded` | Una pasarela degradada no duplica ni convierte incertidumbre en rechazo. | 10 usuarios / 2 usuarios/s / 120s | `EXECUTED` | [evidence/runs/20260912T040738Z/03-gateway-degraded](../evidence/runs/20260912T040738Z/03-gateway-degraded) |
| `04-provider-normalization` | Los estados de tarjeta y banco se normalizan al mismo modelo público. | 10 usuarios / 2 usuarios/s / 120s | `EXECUTED` | [evidence/runs/20260912T040738Z/04-provider-normalization](../evidence/runs/20260912T040738Z/04-provider-normalization) |
| `05-idempotency-duplicates` | Diez solicitudes idénticas concurrentes producen un cobro del proveedor. | 10 usuarios / 2 usuarios/s / 120s | `EXECUTED` | [evidence/runs/20260912T040738Z/05-idempotency-duplicates](../evidence/runs/20260912T040738Z/05-idempotency-duplicates) |
| `06-participant-db-failure` | La caída de participant DB queda aislada y retorna un error explícito de dependencia. | 10 usuarios / 2 usuarios/s / 120s | `EXECUTED` | [evidence/runs/20260912T040738Z/06-participant-db-failure](../evidence/runs/20260912T040738Z/06-participant-db-failure) |

## Interpretación y límites

`PASS` significa que el criterio medido se encontró en los artefactos; `FAIL` significa que hubo evidencia suficiente de incumplimiento; `NOT EXECUTED` significa que faltó el artefacto requerido. No se infieren resultados a partir del código de salida del runner. Las pruebas de cluster, mTLS e Istio solo cuentan cuando sus trazas, métricas y eventos están presentes.
