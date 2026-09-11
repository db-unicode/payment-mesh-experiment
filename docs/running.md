# Ejecución en macOS, Linux y Windows

La dependencia local soportada es Docker Compose. Los mismos comandos funcionan en una terminal macOS, una shell Linux o Windows WSL2 (activa la integración WSL de Docker Desktop). OrbStack puede sustituir Docker Desktop en macOS.

## Compose local

```sh
mise install                 # instala las versiones fijadas
mise run doctor
mise run setup               # certificados TLS locales + .venv/Locust
mise run up
curl http://localhost:8080/healthz
curl -X POST http://localhost:8080/v1/payments \
  -H 'content-type: application/json' -H 'Idempotency-Key: demo-1' \
  -d '{"debtor_participant_id":"participant-card","creditor_participant_id":"participant-bank","amount_minor":1200,"currency":"COP","reference":"demo-1"}'
mise run experiment-all
mise run report
mise run down
```

Sin `mise`, usa `go test ./...`, `docker compose -f deploy/docker/docker-compose.yml up -d --build` y las órdenes equivalentes de `mise.toml`. En Windows, ejecútalas dentro de WSL2; `kubectl` y `docker` deben apuntar al mismo contexto de Docker Desktop.

## Kubernetes

Construye las tres imágenes (`payment-operator`, `payment-router`, `payment-participant-manager`) y cárgalas en kind, o usa el almacén local de OrbStack. Ejecuta `mise run k8s-kind` o `mise run k8s-orbstack`. Instala Istio con `istioctl install -f deploy/istio/istio-operator.yaml`, aplica `kubectl apply -k deploy/observability`, habilita ingress/egress y aplica `kubectl apply -k deploy/istio`.

Después de crear los certificados con `mise run setup`, registra la CA del mock para la originación TLS del egress gateway:

```sh
kubectl -n istio-system create secret generic gateway-ca \
  --from-file=ca.crt=deploy/docker/certs/tls.crt \
  --dry-run=client -o yaml | kubectl apply -f -
```

Los manifests solicitan tres PVC ligeros de 256 MiB, uno por bounded context, para conservar el aislamiento y permitir reinicios durante los experimentos. Compose usa tres volúmenes nombrados.

En OrbStack, el ingress suele estar disponible en el NodePort local. Obtén el puerto y ejecuta los seis experimentos así:

```sh
kubectl get svc istio-ingressgateway -n istio-system
BASE_URL=http://127.0.0.1:<node-port> INGRESS_HOST=payments.local mise run experiment-all
mise run report
```

## Gateway scenarios

Los mocks sirven TLS en `https://localhost:8091` (card) y `https://localhost:8092` (bank). El router local permite el certificado autofirmado mediante `GATEWAY_TLS_INSECURE=true`; en Kubernetes la aplicación habla HTTP hasta Istio y el egress gateway origina TLS hacia los mocks del host (`host.docker.internal`). Cambia un comportamiento con `curl -k -X POST https://localhost:8091/admin/behavior -H 'content-type: application/json' -d '{"behavior":"timeout"}'`. Los contratos son distintos: card usa `/v1/card/authorizations` y estados `AUTHORIZED/DECLINED`; bank usa `/v2/transfers` y estados `ACCEPTED/REJECTED`.
