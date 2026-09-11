# Payment Mesh Experiment

Experimento académico reproducible para estudiar bounded contexts, idempotencia persistente, selección automática de pasarela, service mesh zero-trust y resiliencia.

## Inicio rápido

Requisitos: Docker Desktop/OrbStack (macOS), Docker Engine (Linux), o Docker Desktop + WSL2 (Windows). `mise` es opcional pero recomendado.

```sh
mise run up          # PostgreSQL aislado + mocks de pasarela
mise run test        # pruebas unitarias
mise run report      # genera report.md a partir de evidencia
```

Para ejecutar en Kubernetes: `mise run k8s-kind` o `mise run k8s-orbstack`; Istio se instala por separado según `docs/running.md`.

La evidencia generada se guarda bajo `evidence/` y el informe reproducible en `report.md` (no se versiona por defecto).

## Servicios

- `payment-operator`: API pública, autorización de la orden y normalización de estado.
- `participant-payment-manager`: datos de participantes e instrumento de pago.
- `payment-router`: idempotencia y routing a la pasarela correcta.
- `gateway-card` / `gateway-bank`: mocks externos en Docker Compose, fuera de Kubernetes.

Cada servicio tiene una base PostgreSQL dedicada. El mesh añade mTLS STRICT, políticas de autorización, egress gateway y circuit breaker.

Consulta [docs/architecture.md](docs/architecture.md), [docs/running.md](docs/running.md) y [docs/experiments.md](docs/experiments.md).
