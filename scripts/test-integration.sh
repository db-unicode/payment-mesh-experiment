#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
RUNTIME_IMAGE="${INTEGRATION_RUNTIME_IMAGE:-}"
GO_ARCH="${INTEGRATION_GOARCH:-}"
NETWORK="payment-integration-$$"
ROUTER_DB="${NETWORK}-router-db"
OPERATOR_DB="${NETWORK}-operator-db"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/payment-integration.XXXXXX")
ROUTER_BIN="$TMP_DIR/payment-router.test"
OPERATOR_BIN="$TMP_DIR/payment-operator.test"
GO_CACHE="${INTEGRATION_GOCACHE:-$TMP_DIR/go-cache}"

cleanup() {
	docker rm -f "$ROUTER_DB" "$OPERATOR_DB" >/dev/null 2>&1 || true
	docker network rm "$NETWORK" >/dev/null 2>&1 || true
	rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

if [ -z "$RUNTIME_IMAGE" ] && docker image inspect payment-mesh-experiment-payment-router:latest >/dev/null 2>&1; then
	RUNTIME_IMAGE=payment-mesh-experiment-payment-router:latest
fi
if [ -z "$RUNTIME_IMAGE" ]; then
	RUNTIME_IMAGE=payment-router:dev
fi
docker image inspect "$RUNTIME_IMAGE" >/dev/null
if [ -z "$GO_ARCH" ]; then
	GO_ARCH=$(docker image inspect "$RUNTIME_IMAGE" --format '{{.Architecture}}')
fi
case "$GO_ARCH" in
	amd64|arm64) ;;
	*) echo "Unsupported runtime image architecture: $GO_ARCH" >&2; exit 1 ;;
esac
docker network create "$NETWORK" >/dev/null
docker run -d --rm --name "$ROUTER_DB" --network "$NETWORK" \
	-e POSTGRES_DB=router -e POSTGRES_USER=router -e POSTGRES_PASSWORD=router postgres:16-alpine >/dev/null
docker run -d --rm --name "$OPERATOR_DB" --network "$NETWORK" \
	-e POSTGRES_DB=operator -e POSTGRES_USER=operator -e POSTGRES_PASSWORD=operator postgres:16-alpine >/dev/null

wait_for_postgres() {
	container="$1"; user="$2"; database="$3"
	i=0
	while [ "$i" -lt 30 ]; do
		if docker exec "$container" pg_isready -U "$user" -d "$database" >/dev/null 2>&1; then
			return 0
		fi
		i=$((i + 1))
		sleep 1
	done
	echo "PostgreSQL did not become ready: $container" >&2
	return 1
}

wait_for_postgres "$ROUTER_DB" router router
wait_for_postgres "$OPERATOR_DB" operator operator

GOCACHE="$GO_CACHE" CGO_ENABLED=0 GOOS=linux GOARCH="$GO_ARCH" \
	go test -count=1 -c "$ROOT_DIR/cmd/payment-router" -o "$ROUTER_BIN"
GOCACHE="$GO_CACHE" CGO_ENABLED=0 GOOS=linux GOARCH="$GO_ARCH" \
	go test -count=1 -c "$ROOT_DIR/cmd/payment-operator" -o "$OPERATOR_BIN"

docker run --rm --network "$NETWORK" \
	-e ROUTER_TEST_DATABASE_URL="postgres://router:router@$ROUTER_DB:5432/router?sslmode=disable" \
	-v "$ROUTER_BIN:/router.test:ro" --entrypoint /router.test "$RUNTIME_IMAGE" \
	-test.run 'Test(Create|FailedResultUpdate)' -test.v
docker run --rm --network "$NETWORK" \
	-e OPERATOR_TEST_DATABASE_URL="postgres://operator:operator@$OPERATOR_DB:5432/operator?sslmode=disable" \
	-v "$OPERATOR_BIN:/operator.test:ro" --entrypoint /operator.test "$RUNTIME_IMAGE" \
	-test.run TestPublicReplayReconcilesPendingOrderAndUsesFrozenRouterRequest -test.v

echo "Integration tests passed using isolated Docker network $NETWORK"
