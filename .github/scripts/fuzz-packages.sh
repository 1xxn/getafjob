#!/usr/bin/env bash
# Lists the package dirs (./path) that contain fuzz targets, one per line; with --json as a JSON array for a
# GitHub Actions matrix.
set -euo pipefail
mapfile -t dirs < <(grep -rlE '^func Fuzz[A-Za-z0-9_]*\(' --include='*_test.go' . | xargs -n1 dirname | sort -u)
if [ "${1:-}" = "--json" ]; then
  json=$(printf '"%s",' "${dirs[@]}")
  echo "[${json%,}]"
else
  printf '%s\n' "${dirs[@]}"
fi
