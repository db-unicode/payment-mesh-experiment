# Running on macOS, Linux and Windows

The supported local dependency is Docker Compose. The same commands work in a macOS terminal, a Linux shell, or Windows WSL2 (use Docker Desktop's WSL integration). OrbStack can replace Docker Desktop on macOS.

## Local Compose

```sh
mise install                 # optional: installs the pinned Go version
mise run up
curl http://localhost:8080/healthz
curl -X POST http://localhost:8080/v1/payments \
  -H 'content-type: application/json' -H 'Idempotency-Key: demo-1' \
  -d '{"amount_minor":1200,"currency":"COP","instrument":"card","participant_id":"participant-card"}'
mise run load
mise run report
mise run down
```

Without `mise`, use `go test ./...`, `docker compose -f deploy/docker/docker-compose.yml up -d --build`, and the corresponding commands in `mise.toml`. On Windows, run them inside WSL2; `kubectl` and `docker` must point to the same Docker Desktop context.

## Kubernetes

Build the three images (`payment-operator`, `payment-router`, `payment-participant-manager`) and load them into kind, or use OrbStack's local image store. Then run `mise run k8s-kind` or `mise run k8s-orbstack`. Install Istio, enable the ingress and egress gateways, and apply `kubectl apply -k deploy/istio`.

The base manifests intentionally use `emptyDir` for PostgreSQL to keep the experiment light; this is not production persistence. Compose uses named volumes. Cluster experiments must be run on a host with a working Kubernetes runtime and are not claimed as executed by this repository.

## Gateway scenarios

The card mock is available at `http://localhost:8091` and bank at `http://localhost:8092`. Change a behavior with `curl -X POST localhost:8091/admin/behavior -H 'content-type: application/json' -d '{"behavior":"timeout"}'`. Valid behaviors are `success`, `decline`, and `error`; the runner resets them per scenario.
