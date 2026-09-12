#!/usr/bin/env sh
set -eu

kubectl apply -f deploy/observability/namespace.yaml
if ! kubectl get secret grafana-admin -n observability >/dev/null 2>&1; then
  # Never print or commit the administrator credential. Anonymous access is
  # Viewer-only; the Service is cluster-local and exposed via port-forward.
  openssl rand -hex 32 | kubectl create secret generic grafana-admin \
    -n observability --from-file=admin-password=/dev/stdin
fi
kubectl apply -k deploy/observability
kubectl rollout status deployment/grafana -n observability --timeout=180s
