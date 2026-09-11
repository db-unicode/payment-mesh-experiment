#!/usr/bin/env sh
set -eu
PROJECT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
CERT_DIR="$PROJECT_DIR/deploy/docker/certs"
mkdir -p "$CERT_DIR"
if [ ! -s "$CERT_DIR/tls.crt" ] || [ ! -s "$CERT_DIR/tls.key" ]; then
  openssl req -x509 -nodes -newkey rsa:2048 -days 30 \
    -keyout "$CERT_DIR/tls.key" -out "$CERT_DIR/tls.crt" \
    -subj "/CN=payment-gateway" \
    -addext "subjectAltName=DNS:gateway-card,DNS:gateway-bank,DNS:gateway-card.mesh.local,DNS:gateway-bank.mesh.local"
fi
chmod 600 "$CERT_DIR/tls.key"
echo "Certificados locales creados en $CERT_DIR"
