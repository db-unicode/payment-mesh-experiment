#!/usr/bin/env sh
set -eu

PROJECT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
ENV_FILE="$PROJECT_DIR/.env"

if [ ! -e "$ENV_FILE" ]; then
  (umask 077; : > "$ENV_FILE")
fi

# Keep credentials out of tracked manifests. A developer may provide either
# value explicitly; otherwise generate random values once for this workspace.
if [ -z "${GATEWAY_PAYMENT_TOKEN:-}" ] && ! grep -Eq '^GATEWAY_PAYMENT_TOKEN=[^[:space:]]+' "$ENV_FILE" 2>/dev/null; then
  payment_token="$(openssl rand -hex 32)"
  printf 'GATEWAY_PAYMENT_TOKEN=%s\n' "$payment_token" >> "$ENV_FILE"
elif [ -n "${GATEWAY_PAYMENT_TOKEN:-}" ] && ! grep -Eq '^GATEWAY_PAYMENT_TOKEN=[^[:space:]]+' "$ENV_FILE" 2>/dev/null; then
  printf 'GATEWAY_PAYMENT_TOKEN=%s\n' "$GATEWAY_PAYMENT_TOKEN" >> "$ENV_FILE"
fi
if [ -z "${GATEWAY_ADMIN_TOKEN:-}" ] && ! grep -Eq '^GATEWAY_ADMIN_TOKEN=[^[:space:]]+' "$ENV_FILE" 2>/dev/null; then
  admin_token="$(openssl rand -hex 32)"
  printf 'GATEWAY_ADMIN_TOKEN=%s\n' "$admin_token" >> "$ENV_FILE"
elif [ -n "${GATEWAY_ADMIN_TOKEN:-}" ] && ! grep -Eq '^GATEWAY_ADMIN_TOKEN=[^[:space:]]+' "$ENV_FILE" 2>/dev/null; then
  printf 'GATEWAY_ADMIN_TOKEN=%s\n' "$GATEWAY_ADMIN_TOKEN" >> "$ENV_FILE"
fi
chmod 600 "$ENV_FILE"

if [ "${1:-}" = "--kubernetes" ]; then
  # Compose reads .env automatically; Kubernetes needs the same token in a
  # namespace-local Secret. The value is never emitted to stdout or YAML logs.
  payment_token="$(sed -n 's/^GATEWAY_PAYMENT_TOKEN=//p' "$ENV_FILE" | sed -n '1p')"
  if [ -z "$payment_token" ]; then
    echo "GATEWAY_PAYMENT_TOKEN is missing from $ENV_FILE" >&2
    exit 1
  fi
  kubectl -n payments create secret generic gateway-auth \
    --from-literal="GATEWAY_PAYMENT_TOKEN=$payment_token" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
fi

echo "Gateway credentials are ready in $ENV_FILE (mode 600)."
