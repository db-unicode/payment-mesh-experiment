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

Sin `mise`, usa `go test ./...`, `docker compose --env-file .env -f deploy/docker/docker-compose.yml up -d --build` y las órdenes equivalentes de `mise.toml`. En Windows, ejecútalas dentro de WSL2; `kubectl` y `docker` deben apuntar al mismo contexto de Docker Desktop. El apagado normal conserva los volúmenes de PostgreSQL y de las pasarelas.

## Kubernetes

Construye las tres imágenes (`payment-operator`, `payment-router`, `payment-participant-manager`) y cárgalas en kind, o usa el almacén local de OrbStack. Ejecuta `mise run k8s-kind` o `mise run k8s-orbstack`. Instala Istio con `istioctl install -f deploy/istio/istio-operator.yaml`, aplica `kubectl apply -k deploy/observability`, habilita ingress/egress y aplica `kubectl apply -k deploy/istio`.

Después de crear los certificados con `mise run setup`, registra la CA del mock para la originación TLS del egress gateway:

```sh
kubectl -n istio-system create secret generic gateway-ca \
  --from-file=ca.crt=deploy/docker/certs/tls.crt \
  --dry-run=client -o yaml | kubectl apply -f -
```

Cuando exista el namespace `payments`, ejecuta `mise run k8s-secrets` para entregar al router la credencial de las pasarelas. No copies los tokens a manifiestos ni al informe.

Para que las pasarelas externas exporten al mismo Collector de Kubernetes que Istio, levántalas con:

```sh
GATEWAY_OTEL_ENDPOINT=http://host.docker.internal:30418 mise run up
```

El Collector expone OTLP/HTTP en el NodePort 30418 para este entorno local. En Compose sin Kubernetes se usa `http://otel-collector:4318`, disponible después de `mise run observability-up`.

Los manifests solicitan tres PVC ligeros de 256 MiB, uno por bounded context, para conservar el aislamiento y permitir reinicios durante los experimentos. Compose usa tres volúmenes nombrados.

En OrbStack, el ingress suele estar disponible en el NodePort local. Obtén el puerto y ejecuta los seis experimentos así:

```sh
kubectl get svc istio-ingressgateway -n istio-system
BASE_URL=http://127.0.0.1:<node-port> INGRESS_HOST=payments.local mise run experiment-all
mise run report
```

Para observar la corrida en Kiali, instálalo una vez y deja el port-forward abierto en otra terminal:

```bash
mise run kiali-install
mise run kiali-ui
```

Abre `http://127.0.0.1:20001`; Kiali queda conectado al Prometheus y Jaeger del namespace `observability`. En **Graph**, selecciona el namespace `payments` y un intervalo que incluya la corrida.

Diagnóstico del scraping y verificación real de mTLS: [Prometheus y mTLS](prometheus-mtls.md).

Grafana con paneles de demostración: `mise run grafana-install`, después `mise run kiali-install` y, en otra terminal, `mise run grafana-ui`. Abre `http://localhost:3000/d/payment-mesh`. Consulta [los paneles y sus límites](grafana.md).

## Gateway scenarios

Los mocks sirven TLS en `https://localhost:8091` (card) y `https://localhost:8092` (bank). El router local permite el certificado autofirmado mediante `GATEWAY_TLS_INSECURE=true`; en Kubernetes la aplicación habla HTTP hasta Istio y el egress gateway origina TLS hacia los mocks del host (`host.docker.internal`). Los controles `/admin/behavior` y `/stats` requieren `X-Gateway-Admin`; el runner lee su credencial de `.env` automáticamente. Las rutas de pago requieren `X-Gateway-Auth`, añadido por el router. Los contratos son distintos: card usa `/v1/card/authorizations` y estados `AUTHORIZED/DECLINED`; bank usa `/v2/transfers` y estados `ACCEPTED/REJECTED`.

## Pruebas de código e integración

`mise run test` ejecuta las pruebas de Go. `mise run test-integration` ejecuta además las pruebas de PostgreSQL con contenedores y una red temporales, independientes de las bases de datos del experimento. Requiere Docker activo y una imagen de ejecución local; el script muestra los casos ejecutados y elimina sus recursos temporales al terminar.

Las regresiones cubren duplicados concurrentes, conflicto de contenido, conservación de `PENDING` cuando falla la escritura del resultado, recuperación con la misma clave y reconciliación desde el operador. La suite incluye pruebas de roles, autenticación, errores normalizados y exportación OTLP con el mismo contexto de traza.
