# Architecture and diagram review

The supplied happy-path diagram captures the domain flow, but it places the client directly beside the bounded context and leaves the external gateway and platform controls implicit. The implementation makes these boundaries explicit:

```text
Client -> Istio ingress gateway -> payment-operator -> participant-payment-manager
                                               \-> payment-router -> Istio egress gateway -> gateway-card
                                                                                         \-> gateway-bank
             each workload has a dedicated PostgreSQL database
             OTel/Envoy telemetry -> Collector -> Jaeger; /metrics -> Prometheus
```

The corrections are deliberate:

1. Ingress is the only public entry point in Kubernetes. The operator owns the public contract (`POST /v1/payments`, `GET /v1/payments/{id}`), while router and participant manager expose internal paths.
2. The router makes the gateway choice from the payment instrument (`card` and `wallet` use `gateway-card`; `bank_transfer`, `bank`, and `pse` use `gateway-bank`). The client never chooses a provider, which prevents a provider-specific concern leaking into the API.
3. Card and bank gateways are separate mocks outside Kubernetes. This makes egress, TLS origination, timeout and provider-failure scenarios observable and keeps the external-system boundary honest.
4. There are three PostgreSQL instances: operator orders, participant routing data, and router idempotency/payment state. This preserves bounded-context ownership and lets the experiment stop one datastore without silently sharing state.
5. Istio provides mTLS STRICT, authorization policies, ingress routing, an egress gateway, TLS origination, and a five-failure/30-second outlier policy. The Go router also has a small local circuit guard so local Compose runs preserve the same pending semantics even without Envoy.
6. OTel Collector, Jaeger, Prometheus and optional Kiali are platform dependencies. The services expose Prometheus text metrics and forward W3C trace headers; the mesh supplies the complete request spans once sidecars are enabled.

## Trade-offs

`net/http` and a single pgx driver keep the academic implementation portable. PostgreSQL advisory locks make concurrent idempotency deterministic across router replicas. A timeout or 5xx is `PENDING` because the provider may have charged even when the response was lost; only a definitive provider decline is `FAILED`. Reconciliation is intentionally out of scope, so `PENDING` is documented rather than silently retried forever.
