#!/usr/bin/env bash
# Smoke test for the running Compose stack (go tool task dev:up): TLS front door, security headers, API,
# hidden readiness details and the egress policy (no private networks, no cloud metadata, no direct internet).
set -euo pipefail
project_arg=()
if [ -n "${COMPOSE_PROJECT_NAME:-}" ]; then
  project_arg=(-p "$COMPOSE_PROJECT_NAME")
fi
compose=(docker compose ${COMPOSE_FILES:--f deploy/compose.yaml} "${project_arg[@]}")
base="${GETAFJOB_BASE_URL:-https://localhost:8443}"
fail=0
ok() { echo "ok   $*"; }
bad() { echo "FAIL $*"; fail=1; }

[ "$(curl -fsSk "$base/healthz")" = "ok" ] && ok "healthz through caddy" || bad "healthz through caddy"
curl -fsSk "$base/api/v1/meta" | grep -q '"api":"v1"' && ok "api/v1/meta" || bad "api/v1/meta"
curl -fsSk "$base/login" | grep -q 'action="/login"' && ok "login page" || bad "login page"
code="$(curl -sk -o /dev/null -w '%{http_code}' "$base/readyz")"
[ "$code" = "404" ] && ok "readyz hidden from the outside" || bad "readyz answered $code from the outside"
headers="$(curl -sSk -D - -o /dev/null "$base/healthz")"
for h in content-security-policy strict-transport-security x-frame-options x-content-type-options; do
  grep -qi "^$h:" <<<"$headers" && ok "header $h" || bad "header $h missing"
done

# CONNECT through the egress proxy from inside the runner; prints the proxy's status line.
probe() {
  "${compose[@]}" exec -T runner bash -c \
    'exec 3<>/dev/tcp/egress/4750; printf "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n" "$1" "$1" >&3; timeout 10 head -n1 <&3' \
    _ "$1" | tr -d '\r'
}
for target in 169.254.169.254:80 serve:8080 postgres:5432 127.0.0.1:8081 10.0.0.1:80 "[::1]:80"; do
  status="$(probe "$target" || true)"
  case "$status" in
    *" 200 "*) bad "egress allowed $target" ;;
    *) ok "egress denied $target (${status:-no answer})" ;;
  esac
done
if "${compose[@]}" exec -T runner bash -c 'timeout 5 bash -c "exec 3<>/dev/tcp/1.1.1.1/443"' 2>/dev/null; then
  bad "runner reaches the internet without the proxy"
else
  ok "runner has no direct internet access"
fi
exit "$fail"
