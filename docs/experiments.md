# Protocolo experimental

El perfil predeterminado es 10 usuarios, 2 usuarios/s y 120 segundos; se puede parametrizar con `LOCUST_USERS`, `LOCUST_SPAWN_RATE` y `LOCUST_DURATION`. Cada corrida escribe `evidence/runs/<timestamp>/<experimento>/`.

| ID | Experimento | Hipótesis | Evidencia/criterio |
|---|---|---|---|
| 1 | Happy mesh | ingress, mTLS y tracing acompañan el happy path | `SUCCEEDED`, p95, trazas, secretos/certificados activos y listener mTLS |
| 2 | Fallo de instancia | eliminar una réplica de router no produce errores de transporte/5xx; un resultado incierto puede quedar `PENDING` | respuestas HTTP 200/202, p95 y eventos de Kubernetes |
| 3 | Pasarela degradada | 5xx/timeout quedan `PENDING` y el breaker evita una cascada | estados, p95, métricas de breaker |
| 4 | Normalización multi-proveedor | dos contratos y estados de proveedor producen un modelo común | card/bank y `SUCCEEDED/FAILED/PENDING` |
| 5 | Duplicados | diez solicitudes idénticas generan un único cobro efectivo | `effective_charges` del mock y diez respuestas |
| 6 | Aislamiento de DB | caída de participant DB afecta solo la dependencia de participantes | HTTP/eventos; router DB permanece intacta |

El runner ejecuta inyección de pod/DB únicamente si existe un contexto con los deployments esperados. Cuando no existe, crea `kubernetes.skip` y el veredicto `NOT_EXECUTED`. `docs/report.md` se genera desde `metadata.json` y `verdict.json` con hipótesis, métrica, criterio, resultado y enlace a la evidencia.
