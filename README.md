# Experimento Payment Mesh

Implementación académica reproducible para estudiar bounded contexts, idempotencia persistente, selección de pasarela, zero-trust con Istio y resiliencia.

## Contrato público

`POST /v1/payments` requiere `Idempotency-Key` y recibe únicamente:

```json
{"debtor_participant_id":"participant-card","creditor_participant_id":"participant-bank","amount_minor":1200,"currency":"COP","reference":"invoice-1"}
```

El cliente no envía instrumento ni pasarela. `payment-operator` consulta ambos participantes, deriva el instrumento del deudor y genera un contrato interno separado para `payment-router`.

## Inicio rápido

Requisitos: Docker Desktop/OrbStack (macOS), Docker Engine (Linux) o Docker Desktop con WSL2 (Windows), Go fijado por `mise` y Python 3 para Locust.

```sh
mise run doctor
mise run setup
mise run up
mise run observability-up
mise run experiment-all
mise run report
```

El informe queda en [docs/report.md](docs/report.md). La evidencia se organiza en `evidence/runs/<timestamp>/<experimento>/`; nunca se declaran resultados de cluster si los comandos no se ejecutaron.

Consulta [docs/architecture.md](docs/architecture.md), [docs/running.md](docs/running.md) y [docs/experiments.md](docs/experiments.md).
