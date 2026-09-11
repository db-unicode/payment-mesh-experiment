# Informe del experimento Payment Mesh

Generado: `2026-09-11T01:01:45Z`

## Objetivo y método

Se evalúan seis hipótesis sobre bounded contexts, zero-trust, resiliencia, normalización multi-proveedor, idempotencia y aislamiento de base de datos. El perfil predeterminado es 10 usuarios, incremento de 2 usuarios/s y 120 segundos; cada ejecución conserva metadatos, CSV/HTML de Locust, métricas, trazas, contadores, eventos y logs.

Ejecución más reciente: `evidence/runs/20260911T010124Z`.

## Resultados por hipótesis

| Experimento | Hipótesis | Métrica | Criterio | Resultado | Evidencia |
|---|---|---|---|---|---|
| `01-happy-mesh` | Una orden válida atraviesa ingress, mTLS y tracing. | status, p95, traces | SUCCEEDED and a trace is present | **EXECUTED**: Load test completed with exit code 0 | [evidence/runs/20260911T010124Z/01-happy-mesh/](evidence/runs/20260911T010124Z/01-happy-mesh/) |
| `02-instance-failure` | Eliminar una réplica no interrumpe el servicio. | availability, p95, events | No failed requests beyond the replacement window | **NOT_EXECUTED**: Pod-failure command and load were run only when Kubernetes was available. | [evidence/runs/20260911T010124Z/02-instance-failure/](evidence/runs/20260911T010124Z/02-instance-failure/) |
| `03-gateway-degraded` | Una pasarela degradada no duplica ni convierte incertidumbre en rechazo. | PENDING, breaker, p95 | PENDING and breaker evidence | **EXECUTED**: Load test completed with exit code 0 | [evidence/runs/20260911T010124Z/03-gateway-degraded/](evidence/runs/20260911T010124Z/03-gateway-degraded/) |
| `04-provider-normalization` | Card and bank provider states map to the same API statuses. | provider state, normalized status | AUTHORIZED/ACCEPTED become SUCCEEDED; DECLINED/REJECTED become FAILED | **EXECUTED**: Provider adapter responses were captured for card and bank. | [evidence/runs/20260911T010124Z/04-provider-normalization/](evidence/runs/20260911T010124Z/04-provider-normalization/) |
| `05-idempotency-duplicates` | Ten identical concurrent requests produce one provider charge. | gateway effective_charges, HTTP responses | effective_charges increases by one | **EXECUTED**: Ten responses and provider counters were captured; inspect effective_charges for the verdict. | [evidence/runs/20260911T010124Z/05-idempotency-duplicates/](evidence/runs/20260911T010124Z/05-idempotency-duplicates/) |
| `06-participant-db-failure` | Participant DB failure is isolated and returns an explicit dependency error. | availability, HTTP status, events | Router/payment state remains intact and participant dependency is unavailable | **NOT_EXECUTED**: Participant DB fault injection was performed only when Kubernetes was available. | [evidence/runs/20260911T010124Z/06-participant-db-failure/](evidence/runs/20260911T010124Z/06-participant-db-failure/) |

## Interpretación y límites

Un resultado `EXECUTED` significa que el runner completó sus comandos y conservó sus salidas; no convierte automáticamente una métrica en éxito: el criterio debe revisarse en los archivos de evidencia. `NOT_EXECUTED` identifica inyectores que requieren Kubernetes/Istio y no se inventan datos. El informe no incluye reconciliación asíncrona, colas ni workers.
