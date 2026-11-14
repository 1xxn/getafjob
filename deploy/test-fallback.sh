#!/usr/bin/env bash
# Rehearsal for the maintenance fallback (#518): while serve is stopped, Caddy answers every page with the static
# maintenance page (503, both languages) instead of an error; once serve is back, the app answers again.
# Stops serve of the running stack for a moment: never run it against production.
set -euo pipefail
compose=(docker compose ${COMPOSE_FILES:--f deploy/compose.yaml -f deploy/compose.dev.yaml})
base="${GETAFJOB_BASE_URL:-https://localhost:8443}"
fail=0
ok() { echo "ok   $*"; }
bad() { echo "FAIL $*"; fail=1; }

"${compose[@]}" stop serve >/dev/null
trap '"${compose[@]}" start serve >/dev/null' EXIT

page="$(mktemp)"
code="$(curl -sk -o "$page" -w '%{http_code}' "$base/jobs")"
[ "$code" = "503" ] && ok "fallback answers 503" || bad "fallback answers $code, want 503"
grep -q '<h1>Maintenance</h1>' "$page" && grep -q '<h1>Wartung</h1>' "$page" && ok "fallback page in EN and DE" ||
  bad "fallback page content"
headers="$(curl -sk -D - -o /dev/null "$base/")"
grep -qi '^retry-after:' <<<"$headers" && ok "fallback Retry-After" || bad "fallback Retry-After"
grep -qi "^content-security-policy: default-src 'none'" <<<"$headers" && ok "fallback CSP" || bad "fallback CSP"
grep -qi '^server:' <<<"$headers" && bad "fallback names the server" || ok "fallback hides the server"
rm -f "$page"

"${compose[@]}" start serve >/dev/null
trap - EXIT
for _ in $(seq 1 60); do
  [ "$(curl -sk -o /dev/null -w '%{http_code}' "$base/login")" = "200" ] && break
  sleep 1
done
[ "$(curl -sk -o /dev/null -w '%{http_code}' "$base/login")" = "200" ] && ok "app answers again" || bad "app does not answer again"
exit "$fail"
