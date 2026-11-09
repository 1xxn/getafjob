#!/usr/bin/env bash
# Rollback script: rolls back to the previously recorded version or a specified target version.
#
# Usage:
#   deploy/rollback.sh [OPTIONS] [VERSION]
#
# Examples:
#   deploy/rollback.sh                          # Roll back to the previous successful version
#   deploy/rollback.sh v0.1.0                   # Roll back to a specific version tag
#   deploy/rollback.sh --env staging            # Roll back staging environment
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "$SCRIPT_DIR/deploy.sh" rollback "$@"
