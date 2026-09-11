#!/usr/bin/env sh
set -u
fail=0
for tool in docker kubectl openssl; do
  if command -v "$tool" >/dev/null 2>&1; then echo "OK $tool: $(command -v "$tool")"; else echo "MISSING $tool"; fail=1; fi
done
if command -v docker >/dev/null 2>&1; then docker info --format 'OK docker server: {{.ServerVersion}} {{.OSType}}/{{.Architecture}}' || { echo 'FAIL docker daemon unavailable'; fail=1; }; fi
if command -v kubectl >/dev/null 2>&1; then kubectl config current-context 2>/dev/null || echo 'INFO no Kubernetes context selected'; fi
if command -v python3 >/dev/null 2>&1; then echo "OK python3: $(python3 --version)"; else echo 'MISSING python3'; fail=1; fi
exit "$fail"
