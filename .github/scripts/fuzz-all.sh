#!/usr/bin/env bash
# Runs every Go fuzz target (func FuzzXxx) of the module for the given time each.
set -euo pipefail
fuzztime="${1:-1m}"
found=0
for pkg in $(go list ./...); do
  for target in $(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true); do
    found=1
    echo "== $pkg $target ($fuzztime)"
    go test -run='^$' -fuzz="^${target}\$" -fuzztime="$fuzztime" "$pkg"
  done
done
[ "$found" = 1 ] || echo "no fuzz targets yet"
