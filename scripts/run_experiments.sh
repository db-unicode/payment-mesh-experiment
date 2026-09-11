#!/usr/bin/env sh
set -eu

mkdir -p evidence/raw
: "${BASE_URL:=http://localhost:8080}"
: "${CARD_GATEWAY:=http://localhost:8091}"

run() {
  name="$1"; behavior="$2"; scenario="$3"
  curl -fsS -X POST "${CARD_GATEWAY}/admin/behavior" -H 'content-type: application/json' -d "{\"behavior\":\"${behavior}\"}" >/dev/null || true
  SCENARIO="$scenario" locust -f observability/loadtest/locustfile.py --headless --host "$BASE_URL" --users "${LOCUST_USERS:-10}" --spawn-rate "${LOCUST_SPAWN_RATE:-2}" --run-time "${LOCUST_DURATION:-20s}" --csv "evidence/raw/${name}" --html "evidence/raw/${name}.html" || true
}

# Run the six scenarios one at a time so that evidence has an unambiguous cause.
run 01-happy success happy
run 02-idempotency success idempotency
run 03-definitive-decline decline decline
run 04-uncertain-timeout timeout timeout
run 05-circuit-breaker error breaker
run 06-load-replica-failure success load
