#!/usr/bin/env sh
set -u

BASE_URL="${BASE_URL:-http://localhost:8080}"
INGRESS_HOST="${INGRESS_HOST:-}"
CARD_GATEWAY="${CARD_GATEWAY:-https://localhost:8091}"
BANK_GATEWAY="${BANK_GATEWAY:-https://localhost:8092}"
LOCUST_BIN="${LOCUST_BIN:-.venv/bin/locust}"
USERS="${LOCUST_USERS:-10}"
SPAWN="${LOCUST_SPAWN_RATE:-2}"
DURATION="${LOCUST_DURATION:-120s}"
FAILURE_AT_SECONDS="${FAILURE_AT_SECONDS:-30}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
ROOT="evidence/runs/$STAMP"
mkdir -p "$ROOT"

api_curl() {
  if [ -n "$INGRESS_HOST" ]; then
    curl -H "Host: $INGRESS_HOST" "$@"
  else
    curl "$@"
  fi
}

write_metadata() {
  exp="$1"; hypothesis="$2"; metric="$3"; criterion="$4"
  dir="$ROOT/$exp"; mkdir -p "$dir/events" "$dir/metrics" "$dir/traces" "$dir/logs"
  commit="$(git rev-parse HEAD 2>/dev/null || printf unknown)"
  printf '{"experiment":"%s","hypothesis":"%s","metric":"%s","criterion":"%s","commit":"%s","timestamp":"%s","users":%s,"spawn_rate":%s,"duration":"%s"}\n' "$exp" "$hypothesis" "$metric" "$criterion" "$commit" "$STAMP" "$USERS" "$SPAWN" "$DURATION" > "$dir/metadata.json"
}

capture_common() {
  exp="$1"; dir="$ROOT/$exp"
  api_curl -fsS "$BASE_URL/metrics" > "$dir/metrics/operator.prom" 2> "$dir/logs/operator-metrics.err"; printf '%s\n' "$?" > "$dir/metrics/operator.prom.exit"
  curl -kfsS "$CARD_GATEWAY/stats" > "$dir/metrics/card-stats.json" 2> "$dir/logs/card-stats.err"; printf '%s\n' "$?" > "$dir/metrics/card-stats.json.exit"
  curl -kfsS "$BANK_GATEWAY/stats" > "$dir/metrics/bank-stats.json" 2> "$dir/logs/bank-stats.err"; printf '%s\n' "$?" > "$dir/metrics/bank-stats.json.exit"
  curl -fsS "${JAEGER_URL:-http://localhost:16686}/api/services" > "$dir/traces/jaeger-services.json" 2> "$dir/logs/jaeger.err"; printf '%s\n' "$?" > "$dir/traces/jaeger-services.json.exit"
  curl -fsS "${JAEGER_URL:-http://localhost:16686}/api/traces?service=payment-operator.payments&limit=20" > "$dir/traces/payment-operator.json" 2>> "$dir/logs/jaeger.err"; printf '%s\n' "$?" > "$dir/traces/payment-operator.json.exit"
  curl -fsSG "${PROMETHEUS_URL:-http://localhost:9090}/api/v1/query" --data-urlencode 'query=sum(rate(payments_requests_total[1m])) by (job)' > "$dir/metrics/payment-request-rate.json" 2> "$dir/logs/prometheus.err"; printf '%s\n' "$?" > "$dir/metrics/payment-request-rate.json.exit"
  if command -v kubectl >/dev/null 2>&1 && kubectl get namespace payments >/dev/null 2>&1; then
    kubectl get events -n payments --sort-by=.lastTimestamp > "$dir/events/kubernetes-events.txt" 2> "$dir/logs/kubernetes-events.err"
    kubectl logs -n payments deployment/payment-operator -c app --tail=300 > "$dir/logs/payment-operator.log" 2>&1
    kubectl logs -n payments deployment/payment-router -c app --tail=300 --prefix > "$dir/logs/payment-router.log" 2>&1
    kubectl exec -n istio-system deployment/istio-egressgateway -- pilot-agent request GET stats > "$dir/metrics/egress-envoy-stats.txt" 2> "$dir/logs/egress-stats.err"
    kubectl get peerauthentication,authorizationpolicy -A -o yaml > "$dir/events/zero-trust-policies.yaml" 2> "$dir/logs/zero-trust-policies.err"
    if command -v istioctl >/dev/null 2>&1; then
      istioctl proxy-status > "$dir/events/istio-proxy-status.txt" 2> "$dir/logs/istio-proxy-status.err"
      istioctl proxy-config secret deployment/payment-operator.payments > "$dir/events/operator-mtls-secrets.txt" 2> "$dir/logs/operator-mtls-secrets.err"
      istioctl proxy-config listener deployment/istio-egressgateway.istio-system --port 8080 -o json > "$dir/events/egress-mtls-listener.json" 2> "$dir/logs/egress-mtls-listener.err"
    fi
  fi
}

set_behavior() {
  gateway="$1"; behavior="$2"
  curl -kfsS -X POST "$gateway/admin/behavior" -H 'content-type: application/json' -d "{\"behavior\":\"$behavior\"}" >/dev/null
}

run_load() {
  exp="$1"; scenario="$2"; dir="$ROOT/$exp"
  SCENARIO="$scenario" INGRESS_HOST="$INGRESS_HOST" "$LOCUST_BIN" -f observability/loadtest/locustfile.py --headless --host "$BASE_URL" --users "$USERS" --spawn-rate "$SPAWN" --run-time "$DURATION" --csv "$dir/locust" --html "$dir/locust.html" > "$dir/events/locust.stdout" 2> "$dir/logs/locust.stderr"
  code=$?; printf '%s\n' "$code" > "$dir/events/locust.exit"; return "$code"
}

run_experiment() {
  exp="$1"; hypothesis="$2"; metric="$3"; criterion="$4"; scenario="$5"
  write_metadata "$exp" "$hypothesis" "$metric" "$criterion"
  if run_load "$exp" "$scenario"; then status=EXECUTED; else status=FAILED; fi
  capture_common "$exp"
  printf '{"status":"%s","result":"La carga terminó con código de salida %s","evidence":"%s"}\n' "$status" "$(cat "$ROOT/$exp/events/locust.exit")" "evidence/runs/$STAMP/$exp" > "$ROOT/$exp/verdict.json"
}

# 1. Happy path through ingress/mesh with mTLS and tracing (Kubernetes run required for security verdict).
set_behavior "$CARD_GATEWAY" success; set_behavior "$BANK_GATEWAY" success
run_experiment 01-happy-mesh "Una orden válida atraviesa ingress, mTLS y tracing." "estado, p95, trazas" "SUCCEEDED y existe una traza" happy

# 2. Kill one router instance only when a Kubernetes context is available.
write_metadata 02-instance-failure "Eliminar una réplica no interrumpe el servicio." "disponibilidad, p95, eventos" "No hay fallos fuera de la ventana de reemplazo"
if command -v kubectl >/dev/null 2>&1 && kubectl get deployment payment-router -n payments >/dev/null 2>&1; then
  run_load 02-instance-failure load & load_pid=$!
  sleep "$FAILURE_AT_SECONDS"
  router_pod="$(kubectl get pod -n payments -l app=payment-router --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')"
  kubectl delete pod -n payments "$router_pod" --wait=false > "$ROOT/02-instance-failure/events/kubectl-delete.out" 2> "$ROOT/02-instance-failure/logs/kubectl-delete.err"; k=$?
  wait "$load_pid"; l=$?
  status=EXECUTED
  if [ "$k" -ne 0 ] || [ "$l" -ne 0 ]; then status=FAILED; fi
else
  printf '%s\n' 'NOT EXECUTED: Kubernetes deployment payments/payment-router is unavailable.' > "$ROOT/02-instance-failure/events/kubernetes.skip"
  status=NOT_EXECUTED
fi
capture_common 02-instance-failure
printf '{"status":"%s","result":"Pod-failure command and load were run only when Kubernetes was available.","evidence":"evidence/runs/%s/02-instance-failure"}\n' "$status" "$STAMP" > "$ROOT/02-instance-failure/verdict.json"

# 3. Degraded provider: 5xx/timeout become PENDING and breaker evidence is captured.
set_behavior "$CARD_GATEWAY" error
run_experiment 03-gateway-degraded "Una pasarela degradada no duplica ni convierte incertidumbre en rechazo." "PENDING, circuit breaker, p95" "PENDING y evidencia de apertura del circuito" degraded
set_behavior "$CARD_GATEWAY" timeout
api_curl -sS -o "$ROOT/03-gateway-degraded/events/timeout-response.json" -w '%{http_code}\n' -X POST "$BASE_URL/v1/payments" -H 'content-type: application/json' -H "Idempotency-Key: timeout-$STAMP" -d '{"debtor_participant_id":"participant-card","creditor_participant_id":"participant-bank","amount_minor":1200,"currency":"COP","reference":"timeout-evidence"}' > "$ROOT/03-gateway-degraded/events/timeout-http-status.txt" 2> "$ROOT/03-gateway-degraded/logs/timeout-request.err"
set_behavior "$CARD_GATEWAY" success

# 4. Two real provider contracts normalize to the common status model.
write_metadata 04-provider-normalization "Los estados de tarjeta y banco se normalizan al mismo modelo público." "estado del proveedor, estado normalizado" "AUTHORIZED/ACCEPTED se vuelven SUCCEEDED; DECLINED/REJECTED se vuelven FAILED"
api_curl -fsS -X POST "$BASE_URL/v1/payments" -H 'content-type: application/json' -H "Idempotency-Key: norm-card-$STAMP" -d '{"debtor_participant_id":"participant-card","creditor_participant_id":"participant-bank","amount_minor":1200,"currency":"COP","reference":"normalization-card"}' > "$ROOT/04-provider-normalization/events/card-authorized.json"; c1=$?
api_curl -fsS -X POST "$BASE_URL/v1/payments" -H 'content-type: application/json' -H "Idempotency-Key: norm-bank-$STAMP" -d '{"debtor_participant_id":"participant-bank","creditor_participant_id":"participant-card","amount_minor":1200,"currency":"COP","reference":"normalization-bank"}' > "$ROOT/04-provider-normalization/events/bank-accepted.json"; c2=$?
set_behavior "$CARD_GATEWAY" decline; set_behavior "$BANK_GATEWAY" decline
api_curl -sS -X POST "$BASE_URL/v1/payments" -H 'content-type: application/json' -H "Idempotency-Key: norm-card-decline-$STAMP" -d '{"debtor_participant_id":"participant-card","creditor_participant_id":"participant-bank","amount_minor":1200,"currency":"COP","reference":"normalization-card-decline"}' > "$ROOT/04-provider-normalization/events/card-declined.json"; c3=$?
api_curl -sS -X POST "$BASE_URL/v1/payments" -H 'content-type: application/json' -H "Idempotency-Key: norm-bank-decline-$STAMP" -d '{"debtor_participant_id":"participant-bank","creditor_participant_id":"participant-card","amount_minor":1200,"currency":"COP","reference":"normalization-bank-decline"}' > "$ROOT/04-provider-normalization/events/bank-rejected.json"; c4=$?
set_behavior "$CARD_GATEWAY" success; set_behavior "$BANK_GATEWAY" success
capture_common 04-provider-normalization; status=EXECUTED; [ "$c1" -ne 0 ] && status=FAILED; [ "$c2" -ne 0 ] && status=FAILED
[ "$c3" -ne 0 ] && status=FAILED; [ "$c4" -ne 0 ] && status=FAILED
printf '{"status":"%s","result":"Provider adapter responses were captured for card and bank.","evidence":"evidence/runs/%s/04-provider-normalization"}\n' "$status" "$STAMP" > "$ROOT/04-provider-normalization/verdict.json"

# 5. Exactly ten concurrent requests share one key; gateway stats prove one effective charge.
write_metadata 05-idempotency-duplicates "Diez solicitudes idénticas concurrentes producen un cobro del proveedor." "effective_charges, respuestas HTTP" "effective_charges aumenta exactamente en uno"
key="duplicate-$STAMP"; dir="$ROOT/05-idempotency-duplicates"; pids=""
curl -kfsS "$CARD_GATEWAY/stats" > "$dir/metrics/card-stats-before.json"
for i in 1 2 3 4 5 6 7 8 9 10; do api_curl -sS -X POST "$BASE_URL/v1/payments" -H 'content-type: application/json' -H "Idempotency-Key: $key" -d '{"debtor_participant_id":"participant-card","creditor_participant_id":"participant-bank","amount_minor":1200,"currency":"COP","reference":"duplicate-test"}' > "$dir/events/response-$i.json" 2> "$dir/logs/response-$i.err" & pids="$pids $!"; done
for pid in $pids; do wait "$pid"; done
cksum "$dir"/events/response-*.json > "$dir/events/response-checksums.txt"
capture_common 05-idempotency-duplicates; status=EXECUTED
printf '{"status":"%s","result":"Ten responses and provider counters were captured; inspect effective_charges for the verdict.","evidence":"evidence/runs/%s/05-idempotency-duplicates"}\n' "$status" "$STAMP" > "$dir/verdict.json"

# 6. Participant DB isolation: scale only its database down when Kubernetes is available.
write_metadata 06-participant-db-failure "La caída de participant DB queda aislada y retorna un error explícito de dependencia." "disponibilidad, estado HTTP, eventos" "El estado del router permanece intacto y la dependencia de participantes no está disponible"
if command -v kubectl >/dev/null 2>&1 && kubectl get deployment participant-db -n payments >/dev/null 2>&1; then
  kubectl scale deployment participant-db -n payments --replicas=0 > "$ROOT/06-participant-db-failure/events/kubectl-scale-down.out" 2> "$ROOT/06-participant-db-failure/logs/kubectl-scale-down.err"; down=$?
  kubectl wait --for=delete pod -n payments -l app=participant-db --timeout=120s > "$ROOT/06-participant-db-failure/events/kubectl-wait-down.out" 2> "$ROOT/06-participant-db-failure/logs/kubectl-wait-down.err"; wait_down=$?
  api_curl -sS -o "$ROOT/06-participant-db-failure/events/request.json" -w '%{http_code}\n' -X POST "$BASE_URL/v1/payments" -H 'content-type: application/json' -H "Idempotency-Key: db-failure-$STAMP" -d '{"debtor_participant_id":"participant-card","creditor_participant_id":"participant-bank","amount_minor":1200,"currency":"COP","reference":"db-failure"}' > "$ROOT/06-participant-db-failure/events/http-status.txt" 2> "$ROOT/06-participant-db-failure/logs/request.err"; request=$?
  kubectl exec -n payments deployment/router-db -c postgres -- pg_isready > "$ROOT/06-participant-db-failure/events/router-db-readiness.txt" 2>&1
  kubectl scale deployment participant-db -n payments --replicas=1 > "$ROOT/06-participant-db-failure/events/kubectl-scale-up.out" 2> "$ROOT/06-participant-db-failure/logs/kubectl-scale-up.err"; up=$?
  kubectl rollout status deployment/participant-db -n payments --timeout=180s > "$ROOT/06-participant-db-failure/events/kubectl-rollout.out" 2> "$ROOT/06-participant-db-failure/logs/kubectl-rollout.err"; rollout=$?
  status=EXECUTED
  if [ "$down" -ne 0 ] || [ "$wait_down" -ne 0 ] || [ "$up" -ne 0 ] || [ "$request" -ne 0 ] || [ "$rollout" -ne 0 ]; then status=FAILED; fi
else
  printf '%s\n' 'NOT EXECUTED: Kubernetes deployment payments/participant-db is unavailable.' > "$ROOT/06-participant-db-failure/events/kubernetes.skip"
  status=NOT_EXECUTED
fi
capture_common 06-participant-db-failure
printf '{"status":"%s","result":"Participant DB fault injection was performed only when Kubernetes was available.","evidence":"evidence/runs/%s/06-participant-db-failure"}\n' "$status" "$STAMP" > "$ROOT/06-participant-db-failure/verdict.json"

printf '%s\n' "$ROOT" > evidence/latest-run
echo "Evidencia escrita en $ROOT"
