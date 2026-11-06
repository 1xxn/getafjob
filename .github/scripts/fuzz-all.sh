#!/usr/bin/env bash
# Runs Go fuzz targets (func FuzzXxx) for the given time each: all of the module, or only the given package dirs.
# Targets are found with grep, so nothing is compiled just to list them.
#   fuzz-all.sh 20s                      every target
#   fuzz-all.sh 10m ./internal/matching   the targets of one package
set -euo pipefail
fuzztime="${1:-1m}"
shift || true
dirs=("$@")
if [ ${#dirs[@]} -eq 0 ]; then
  mapfile -t dirs < <(.github/scripts/fuzz-packages.sh)
fi
found=0
for dir in "${dirs[@]}"; do
  for target in $(grep -hoE '^func (Fuzz[A-Za-z0-9_]*)\(' "$dir"/*_test.go | sed -E 's/^func (Fuzz[A-Za-z0-9_]*)\(/\1/' | sort -u); do
    found=1
    echo "== $dir $target ($fuzztime)"
    go test -run='^$' -fuzz="^${target}\$" -fuzztime="$fuzztime" "$dir"
  done
done
[ "$found" = 1 ] || echo "no fuzz targets"
