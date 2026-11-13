#!/usr/bin/env bash
# Deploy workflow script: staging & production deployment, migration, health check, and automatic rollback.
#
# Usage:
#   deploy/deploy.sh [OPTIONS] <VERSION>
#   deploy/deploy.sh rollback [OPTIONS] [VERSION]
#
# Environments:
#   GETAFJOB_ENV=staging     Deploy to staging (COMPOSE_PROJECT_NAME=getafjob-staging, ports 8080/8443)
#   GETAFJOB_ENV=production  Deploy to production (COMPOSE_PROJECT_NAME=getafjob, ports 80/443)
#
# Flow:
#   1. Detect previous version for automatic rollback
#   2. Pull target images (GHCR) and optionally verify cosign signatures
#   3. Run database migrations (expand/contract schema)
#   4. Start services with new images (docker compose up -d)
#   5. Poll health check until healthy
#   6. On failure: automatically roll back to previous image version and restore healthy stack
#   7. On success: record active version in deployment state
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  deploy/deploy.sh [OPTIONS] <VERSION>
  deploy/deploy.sh rollback [OPTIONS] [VERSION]

Options:
  --env <staging|production>  Target environment (default: production, or $GETAFJOB_ENV)
  --project <name>            Docker Compose project name (default: getafjob or getafjob-staging)
  --domain <host>             Domain name (default: localhost, or $GETAFJOB_DOMAIN)
  --secrets-dir <dir>         Secrets directory (default: /etc/getafjob[/staging]/secrets or deploy/secrets)
  --state-dir <dir>           Deployment state directory (default: /var/lib/getafjob/deploy or ./deploy/.state)
  --files <compose-files>     Compose files list (default: "-f deploy/compose.yaml -f deploy/compose.prod.yaml")
  --health-url <url>          Health check URL to poll (default: derived from domain and port)
  --timeout <seconds>         Health check timeout in seconds (default: 60)
  --skip-pull                 Skip docker compose pull (useful for local testing)
  --skip-cosign               Skip cosign image signature verification
  --skip-smoke                Skip smoke test execution after health check
  --help                      Show this help message
EOF
  exit "${1:-1}"
}

ACTION="deploy"
TARGET_VERSION=""
ENV_NAME="${GETAFJOB_ENV:-production}"
PROJECT_NAME="${COMPOSE_PROJECT_NAME:-}"
DOMAIN="${GETAFJOB_DOMAIN:-localhost}"
SECRETS_DIR="${GETAFJOB_SECRETS_DIR:-}"
STATE_DIR="${GETAFJOB_STATE_DIR:-}"
COMPOSE_FILES_STR="${COMPOSE_FILES:--f deploy/compose.yaml -f deploy/compose.prod.yaml}"
HEALTH_URL="${GETAFJOB_HEALTH_URL:-}"
TIMEOUT="${HEALTHCHECK_TIMEOUT:-60}"
SKIP_PULL="${DEPLOY_SKIP_PULL:-false}"
VERIFY_COSIGN="${COSIGN_VERIFY:-auto}"
RUN_SMOKE="${DEPLOY_RUN_SMOKE:-false}"

# Check for 'rollback' subcommand
if [ $# -gt 0 ] && [ "$1" = "rollback" ]; then
  ACTION="rollback"
  shift
fi

while [ $# -gt 0 ]; do
  case "$1" in
    --env)
      ENV_NAME="$2"
      shift 2
      ;;
    --project)
      PROJECT_NAME="$2"
      shift 2
      ;;
    --domain)
      DOMAIN="$2"
      shift 2
      ;;
    --secrets-dir)
      SECRETS_DIR="$2"
      shift 2
      ;;
    --state-dir)
      STATE_DIR="$2"
      shift 2
      ;;
    --files)
      COMPOSE_FILES_STR="$2"
      shift 2
      ;;
    --health-url)
      HEALTH_URL="$2"
      shift 2
      ;;
    --timeout)
      TIMEOUT="$2"
      shift 2
      ;;
    --skip-pull)
      SKIP_PULL="true"
      shift
      ;;
    --skip-cosign)
      VERIFY_COSIGN="false"
      shift
      ;;
    --skip-smoke)
      RUN_SMOKE="false"
      shift
      ;;
    --help|-h)
      usage 0
      ;;
    -*)
      echo "Unknown option: $1" >&2
      usage
      ;;
    *)
      if [ -z "$TARGET_VERSION" ]; then
        TARGET_VERSION="$1"
      else
        echo "Unexpected argument: $1" >&2
        usage
      fi
      shift
      ;;
  esac
done

# If TARGET_VERSION not given as argument, check GETAFJOB_VERSION env
if [ -z "$TARGET_VERSION" ] && [ -n "${GETAFJOB_VERSION:-}" ]; then
  TARGET_VERSION="$GETAFJOB_VERSION"
fi

# Set defaults based on environment
if [ -z "$PROJECT_NAME" ]; then
  if [ "$ENV_NAME" = "staging" ]; then
    PROJECT_NAME="getafjob-staging"
  else
    PROJECT_NAME="getafjob"
  fi
fi

if [ "$ENV_NAME" = "staging" ]; then
  export GETAFJOB_HTTP_PORT="${GETAFJOB_HTTP_PORT:-8080}"
  export GETAFJOB_HTTPS_PORT="${GETAFJOB_HTTPS_PORT:-8443}"
else
  export GETAFJOB_HTTP_PORT="${GETAFJOB_HTTP_PORT:-80}"
  export GETAFJOB_HTTPS_PORT="${GETAFJOB_HTTPS_PORT:-443}"
fi

# Resolve secrets directory
if [ -z "$SECRETS_DIR" ]; then
  if [ "$ENV_NAME" = "staging" ] && [ -d "/etc/getafjob/staging/secrets" ]; then
    SECRETS_DIR="/etc/getafjob/staging/secrets"
  elif [ -d "/etc/getafjob/secrets" ]; then
    SECRETS_DIR="/etc/getafjob/secrets"
  else
    SECRETS_DIR="./secrets"
  fi
fi
if [[ "$SECRETS_DIR" == deploy/* ]]; then
  SECRETS_DIR="./${SECRETS_DIR#deploy/}"
fi
export GETAFJOB_SECRETS_DIR="$SECRETS_DIR"

# Resolve state directory
if [ -z "$STATE_DIR" ]; then
  if [ -d "/var/lib/getafjob" ] && [ -w "/var/lib/getafjob" ]; then
    STATE_DIR="/var/lib/getafjob/deploy"
  elif [ -w "/var/lib" ] 2>/dev/null; then
    STATE_DIR="/var/lib/getafjob/deploy"
  else
    STATE_DIR="./deploy/.state"
  fi
fi
mkdir -p "$STATE_DIR" 2>/dev/null || STATE_DIR="./tmp/deploy-state"
mkdir -p "$STATE_DIR"

STATE_FILE="$STATE_DIR/${PROJECT_NAME}.version"
PREVIOUS_FILE="$STATE_DIR/${PROJECT_NAME}.previous"

# Determine existing / running version
EXISTING_VERSION=""
if [ -f "$STATE_FILE" ]; then
  EXISTING_VERSION="$(head -n1 "$STATE_FILE" | tr -d '\r\n')"
fi
if [ -z "$EXISTING_VERSION" ]; then
  # Try detecting from running docker container
  img="$(docker inspect --format '{{.Config.Image}}' "${PROJECT_NAME}-serve-1" 2>/dev/null || true)"
  if [ -n "$img" ]; then
    EXISTING_VERSION="${img##*:}"
  fi
fi

# Handle rollback action
if [ "$ACTION" = "rollback" ]; then
  if [ -z "$TARGET_VERSION" ]; then
    if [ -f "$PREVIOUS_FILE" ]; then
      TARGET_VERSION="$(head -n1 "$PREVIOUS_FILE" | tr -d '\r\n')"
    elif [ -n "$EXISTING_VERSION" ]; then
      echo "No previous deployment history found in $PREVIOUS_FILE." >&2
      echo "Currently active version is: $EXISTING_VERSION" >&2
      echo "Specify a version to rollback to: deploy/deploy.sh rollback <version>" >&2
      exit 1
    else
      echo "No active or recorded version found for project '$PROJECT_NAME'." >&2
      exit 1
    fi
  fi
  echo "==> Initiating manual rollback to version: $TARGET_VERSION (current: ${EXISTING_VERSION:-none})"
fi

if [ -z "$TARGET_VERSION" ]; then
  echo "Error: target VERSION is required for deployment." >&2
  usage
fi

# Previous version to restore if this deployment fails
ROLLBACK_VERSION="${PREVIOUS_VERSION:-$EXISTING_VERSION}"

# Assemble docker compose command array
read -r -a compose_args <<< "$COMPOSE_FILES_STR"
compose=(docker compose "${compose_args[@]}" -p "$PROJECT_NAME")

# Derive health check URL if not explicitly provided
if [ -z "$HEALTH_URL" ]; then
  HEALTH_URL="https://${DOMAIN}:${GETAFJOB_HTTPS_PORT}/healthz"
fi

export COMPOSE_PROJECT_NAME="$PROJECT_NAME"
export GETAFJOB_DOMAIN="$DOMAIN"
export GETAFJOB_VERSION="$TARGET_VERSION"

echo "========================================================================"
echo " getafjob deploy: $TARGET_VERSION"
echo " Environment:      $ENV_NAME"
echo " Compose Project:  $PROJECT_NAME"
echo " Secrets Dir:      $GETAFJOB_SECRETS_DIR"
echo " State Dir:        $STATE_DIR"
echo " Previous Version: ${ROLLBACK_VERSION:-<none>}"
echo " Health Check URL: $HEALTH_URL"
echo "========================================================================"

trigger_rollback() {
  local reason="$1"
  echo "" >&2
  echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!" >&2
  echo "DEPLOYMENT FAILED: $reason" >&2
  if [ -z "$ROLLBACK_VERSION" ] || [ "$ROLLBACK_VERSION" = "$TARGET_VERSION" ]; then
    echo "No distinct previous version available for automatic rollback." >&2
    echo "Review container logs: ${compose[*]} logs" >&2
    exit 1
  fi

  echo "Initiating automatic rollback to previous version: $ROLLBACK_VERSION..." >&2
  echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!" >&2

  export GETAFJOB_VERSION="$ROLLBACK_VERSION"
  if "${compose[@]}" up -d --remove-orphans --wait; then
    echo "==> Rollback to $ROLLBACK_VERSION completed successfully." >&2
    if [ -n "$HEALTH_URL" ]; then
      if curl -fsSk --connect-timeout 5 "$HEALTH_URL" >/dev/null 2>&1; then
        echo "==> Health check verified for restored version $ROLLBACK_VERSION." >&2
      fi
    fi
    echo "The previous version ($ROLLBACK_VERSION) is active and healthy." >&2
    echo "Inspect logs of failed version $TARGET_VERSION with:" >&2
    echo "  ${compose[*]} logs" >&2
    exit 1
  else
    echo "FATAL: Automatic rollback to $ROLLBACK_VERSION failed!" >&2
    echo "Operator intervention required immediately!" >&2
    exit 2
  fi
}

# 1. Pull images
if [ "$SKIP_PULL" != "true" ]; then
  echo "==> [1/4] Pulling images for $TARGET_VERSION..."
  if ! "${compose[@]}" pull; then
    echo "ERROR: Failed to pull images for version $TARGET_VERSION!" >&2
    # Images not pulled; running stack unchanged
    exit 1
  fi
else
  echo "==> [1/4] Skipping image pull (--skip-pull requested)..."
fi

# Cosign verification of every getafjob image (all three are signed in CI), if requested/available
if [ "$VERIFY_COSIGN" != "false" ]; then
  if command -v cosign >/dev/null 2>&1; then
    echo "==> Verifying cosign signatures for $TARGET_VERSION..."
    for img in "${GETAFJOB_APP_IMAGE:-ghcr.io/1xxn/getafjob}" "${GETAFJOB_RUNNER_IMAGE:-ghcr.io/1xxn/getafjob-runner}" \
      "${GETAFJOB_EGRESS_IMAGE:-ghcr.io/1xxn/getafjob-egress}"; do
      ref="$img:$TARGET_VERSION"
      [[ "$ref" == ghcr.io/* ]] || continue
      if ! cosign verify "$ref" \
        --certificate-identity-regexp '^https://github\.com/1xxn/getafjob/\.github/workflows/' \
        --certificate-oidc-issuer https://token.actions.githubusercontent.com >/dev/null 2>&1; then
        echo "ERROR: Cosign signature verification failed for $ref!" >&2
        exit 1
      fi
      echo "    Signature verified: $ref"
    done
  elif [ "$VERIFY_COSIGN" = "true" ]; then
    echo "ERROR: Cosign verification required (COSIGN_VERIFY=true) but cosign not found on PATH!" >&2
    exit 1
  elif [ "$ENV_NAME" = "production" ]; then
    echo "WARNING: cosign not found: image signatures are NOT verified. Install cosign or set COSIGN_VERIFY=true." >&2
  fi
fi

# 2. Run migrations
echo "==> [2/4] Running database migrations..."
if ! "${compose[@]}" run --rm migrate; then
  echo "ERROR: Database migration failed for version $TARGET_VERSION!" >&2
  # Migrations are expand/contract; existing containers continue running on old schema.
  exit 1
fi

# 3. Bring up new services
echo "==> [3/4] Starting services with $TARGET_VERSION..."
if ! "${compose[@]}" up -d --remove-orphans; then
  trigger_rollback "docker compose up failed"
fi

# 4. Health check
echo "==> [4/4] Verifying health at $HEALTH_URL (timeout: ${TIMEOUT}s)..."
start_time="$(date +%s)"
healthy=0

while [ $(( $(date +%s) - start_time )) -lt "$TIMEOUT" ]; do
  if curl -fsSk --connect-timeout 3 "$HEALTH_URL" >/dev/null 2>&1; then
    healthy=1
    break
  fi
  sleep 2
done

if [ "$healthy" -ne 1 ]; then
  trigger_rollback "Health check failed after ${TIMEOUT}s"
fi

if [ "$RUN_SMOKE" = "true" ]; then
  echo "==> Running smoke test against $HEALTH_URL..."
  export GETAFJOB_BASE_URL="https://${DOMAIN}:${GETAFJOB_HTTPS_PORT}"
  if ! deploy/smoke.sh; then
    trigger_rollback "Smoke test failed"
  fi
fi

# Record deployment state on success
if [ -n "$EXISTING_VERSION" ] && [ "$EXISTING_VERSION" != "$TARGET_VERSION" ]; then
  echo "$EXISTING_VERSION" > "$PREVIOUS_FILE"
fi
echo "$TARGET_VERSION" > "$STATE_FILE"

echo "========================================================================"
echo " Deployment SUCCESSFUL: $TARGET_VERSION active on $ENV_NAME ($PROJECT_NAME)"
echo "========================================================================"
exit 0
