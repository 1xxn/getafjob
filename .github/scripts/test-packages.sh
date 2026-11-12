#!/usr/bin/env bash
# Prints the Go packages of one CI test shard, so the slow ones run in parallel jobs:
#   web  internal/web (the largest package)
#   db   the PostgreSQL adapters and platform/db (testcontainers)
#   rest everything else
set -euo pipefail

mod="$(go list -m)"
pkgs="$(go list ./...)"
db='/adapters/pgstore$|/platform/db$'
case "${1:-}" in
web) echo "$pkgs" | grep -x "$mod/internal/web" ;;
db) echo "$pkgs" | grep -E "$db" ;;
rest) echo "$pkgs" | grep -vx "$mod/internal/web" | grep -vE "$db" ;;
*)
	echo "usage: $0 web|db|rest" >&2
	exit 2
	;;
esac
