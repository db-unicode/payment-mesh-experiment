# Experimental protocol

The default profile uses 10 users, spawn rate 2 users/s, 120 seconds, and two router replicas in Kubernetes; override `LOCUST_USERS`, `LOCUST_SPAWN_RATE`, and `LOCUST_DURATION` for a larger run. This keeps the experiment practical on a competent laptop. Record the exact command, commit, environment, timestamps, and raw Locust CSV/HTML under `evidence/raw/`.

| ID | Scenario | Setup | Expected observation |
|---|---|---|---|
| 1 | Happy path | Both mocks `success` | `SUCCEEDED`, one route selected by instrument |
| 2 | Persistent idempotency | Ten concurrent requests, same key and payload | One effective gateway charge and equivalent responses |
| 3 | Definitive decline | Selected mock `decline` | `FAILED`, no retry loop |
| 4 | Uncertain timeout | Mock delay exceeds 2s client timeout | `PENDING`; no claim that the provider did not charge |
| 5 | Circuit breaker | Five or more 5xx responses, then recovery | Requests become `PENDING` while circuit is open; recovery after 30s |
| 6 | Pod failure under load | 50 users, delete one router pod at t=30s | Service remains available through the other replica; p95 and error rate are recorded |

Acceptance checks are contract tests, duplicate-charge count at the mock, status distribution, p95 latency, and the presence of trace/metric evidence. If Kubernetes or Istio is unavailable, mark rows 5/6 and security observations as `NOT EXECUTED` rather than fabricating results.
