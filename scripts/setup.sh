#!/usr/bin/env sh
set -eu
scripts/bootstrap-certs.sh
scripts/bootstrap-secrets.sh
if command -v python3 >/dev/null 2>&1; then
  python3 -m venv .venv
  .venv/bin/python -m pip install --upgrade pip
  .venv/bin/pip install --require-hashes -r requirements.txt 2>/dev/null || .venv/bin/pip install -r requirements.txt
  echo 'Locust disponible en .venv/bin/locust'
else
  echo 'python3 es requerido para Locust' >&2; exit 1
fi
