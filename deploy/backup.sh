#!/usr/bin/env bash
# Encrypted off-site backup for getafjob (PostgreSQL database + blob store).
# Uses restic natively or via a pinned restic container.
set -euo pipefail

export MSYS_NO_PATHCONV=1

RESTIC_IMAGE="${GETAFJOB_RESTIC_IMAGE:-restic/restic:0.17.3@sha256:8f5a62b422a2cb1277ea0dd6e826fe1acf649e5b9f02d60e5268d5fd1976255a}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/compose.yaml}"
SECRETS_DIR="${GETAFJOB_SECRETS_DIR:-deploy/secrets}"
BACKUP_DIR="${GETAFJOB_BACKUP_DIR:-deploy/backups}"
RESTIC_REPOSITORY="${RESTIC_REPOSITORY:-$BACKUP_DIR/restic}"
INCLUDE_SECRETS="${BACKUP_SECRETS:-0}"
DO_PRUNE="${RESTIC_PRUNE:-1}"
KEEP_DAILY="${RESTIC_KEEP_DAILY:-7}"
KEEP_WEEKLY="${RESTIC_KEEP_WEEKLY:-4}"
KEEP_MONTHLY="${RESTIC_KEEP_MONTHLY:-12}"
EXTRA_TAGS=()

usage() {
  cat <<EOF
Usage: $0 [options]

Create an encrypted off-site backup of PostgreSQL and the blob store using restic.

Options:
  --include-secrets       Include secrets in the restic snapshot (disabled by default)
  --no-prune              Skip retention pruning after backup
  --tag <name>            Additional restic tag (repeatable)
  -h, --help              Show this help

Environment variables:
  RESTIC_REPOSITORY       Restic repository URL or local directory (default: deploy/backups/restic)
  RESTIC_PASSWORD         Encryption password for restic
  RESTIC_PASSWORD_FILE    Path to restic password file (default: deploy/secrets/backup_password if present)
  GETAFJOB_SECRETS_DIR    Directory containing secrets (default: deploy/secrets)
  RESTIC_KEEP_DAILY       Daily snapshots to keep (default: 7)
  RESTIC_KEEP_WEEKLY      Weekly snapshots to keep (default: 4)
  RESTIC_KEEP_MONTHLY     Monthly snapshots to keep (default: 12)
  COMPOSE_FILE            Docker compose file (default: deploy/compose.yaml)
EOF
  exit 0
}

while [ $# -gt 0 ]; do
  case "$1" in
    --include-secrets) INCLUDE_SECRETS=1; shift ;;
    --no-prune) DO_PRUNE=0; shift ;;
    --tag) EXTRA_TAGS+=("$2"); shift 2 ;;
    -h|--help) usage ;;
    *) echo "Unknown option: $1" >&2; usage ;;
  esac
done

compose=(docker compose -f "$COMPOSE_FILE")

# Resolve restic password
if [ -z "${RESTIC_PASSWORD:-}" ]; then
  if [ -n "${RESTIC_PASSWORD_FILE:-}" ] && [ -f "$RESTIC_PASSWORD_FILE" ]; then
    RESTIC_PASSWORD="$(cat "$RESTIC_PASSWORD_FILE")"
  elif [ -f "$SECRETS_DIR/backup_password" ]; then
    RESTIC_PASSWORD="$(cat "$SECRETS_DIR/backup_password")"
  else
    echo "ERROR: Neither RESTIC_PASSWORD nor $SECRETS_DIR/backup_password found." >&2
    echo "Set RESTIC_PASSWORD or generate one: go run ./cmd/getafjob admin gen-secret > $SECRETS_DIR/backup_password" >&2
    exit 1
  fi
fi

# Detect project name from Compose
COMPOSE_PROJECT="$("${compose[@]}" config --format json 2>/dev/null | grep -o '"name": "[^"]*"' | head -n1 | cut -d'"' -f4 || echo "getafjob")"
[ -z "$COMPOSE_PROJECT" ] && COMPOSE_PROJECT="getafjob"

is_remote_repo() {
  case "$RESTIC_REPOSITORY" in
    s3:*|b2:*|sftp:*|rest:*|azure:*|gs:*) return 0 ;;
    *) return 1 ;;
  esac
}

# Run restic commands via Docker container or local binary
run_restic() {
  if [ -n "${RESTIC_BIN:-}" ] || (command -v restic >/dev/null 2>&1 && [ "${GETAFJOB_FORCE_RESTIC_DOCKER:-0}" = "0" ]); then
    RESTIC_PASSWORD="$RESTIC_PASSWORD" RESTIC_REPOSITORY="$RESTIC_REPOSITORY" "${RESTIC_BIN:-restic}" "$@"
  else
    local docker_args=(docker run --rm -i)
    local repo_arg="$RESTIC_REPOSITORY"

    if is_remote_repo; then
      # Remote cloud repository
      for var in AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION AWS_ENDPOINT \
                 B2_ACCOUNT_ID B2_ACCOUNT_KEY AZURE_ACCOUNT_NAME AZURE_ACCOUNT_KEY; do
        if [ -n "${!var:-}" ]; then
          export "${var?}"
          docker_args+=(-e "$var") # by name: the value never shows in the process list
        fi
      done
    else
      # Local directory repository
      mkdir -p "$RESTIC_REPOSITORY"
      local abs_repo
      abs_repo="$(cd "$RESTIC_REPOSITORY" && pwd)"
      docker_args+=(-v "$abs_repo:/repo")
      repo_arg="/repo"
    fi

    if [ -n "${RESTIC_STAGING_DIR:-}" ] && [ -d "$RESTIC_STAGING_DIR" ]; then
      local abs_staging
      abs_staging="$(cd "$RESTIC_STAGING_DIR" && pwd)"
      docker_args+=(-v "$abs_staging:/backup-data:ro")
    fi

    docker_args+=(
      -e "RESTIC_REPOSITORY=$repo_arg"
      -e RESTIC_PASSWORD
      "$RESTIC_IMAGE"
    )
    RESTIC_PASSWORD="$RESTIC_PASSWORD" "${docker_args[@]}" "$@"
  fi
}

echo "=== Starting getafjob backup ==="
echo "Target repository: $RESTIC_REPOSITORY"

# Verify postgres container is responsive
if ! "${compose[@]}" exec -T postgres pg_isready -U getafjob -d getafjob >/dev/null 2>&1; then
  echo "ERROR: PostgreSQL container is not healthy/ready." >&2
  exit 1
fi

# Create temporary staging directory in workspace tmp
mkdir -p tmp
staging_dir="$(mktemp -d tmp/backup-XXXXXX)"
RESTIC_STAGING_DIR="$staging_dir"
cleanup() {
  rm -rf "$staging_dir" 2>/dev/null || docker run --rm -v "$(cd "$(dirname "$staging_dir")" && pwd):/cleanup" alpine rm -rf "/cleanup/$(basename "$staging_dir")" 2>/dev/null || true
}
trap cleanup EXIT

echo "1/4 Dumping PostgreSQL database..."
dump_container_path="/var/lib/postgresql/getafjob_dump.tmp"
"${compose[@]}" exec -T postgres pg_dump -U getafjob -d getafjob --format=custom -f "$dump_container_path"
"${compose[@]}" cp "postgres:$dump_container_path" "$staging_dir/postgres.dump"
"${compose[@]}" exec -T postgres rm -f "$dump_container_path"

echo "2/4 Snapshotting blob volume..."
mkdir -p "$staging_dir/blobs"
docker run --rm \
  -v "${COMPOSE_PROJECT}_blobs:/src:ro" \
  -v "$(cd "$staging_dir/blobs" && pwd):/dst" \
  alpine sh -c "cp -a /src/. /dst/ && chmod -R a+rwX /dst"

# Write backup manifest
timestamp="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
schema_version="$("${compose[@]}" exec -T postgres psql -U getafjob -d getafjob -t -c "SELECT coalesce(max(version_id)::text, '0') FROM goose_db_version;" | tr -d '[:space:]')"
cat <<EOF > "$staging_dir/manifest.json"
{
  "project": "$COMPOSE_PROJECT",
  "created_at": "$timestamp",
  "schema_version": "$schema_version",
  "includes_secrets": $INCLUDE_SECRETS
}
EOF

if [ "$INCLUDE_SECRETS" = "1" ]; then
  echo "    Adding secrets to backup..."
  mkdir -p "$staging_dir/secrets"
  if [ -d "$SECRETS_DIR" ]; then
    cp -a "$SECRETS_DIR/." "$staging_dir/secrets/"
    chmod -R 0600 "$staging_dir/secrets" 2>/dev/null || true
  fi
fi

echo "3/4 Creating encrypted restic snapshot..."
# Initialize repo if needed
if ! run_restic cat config >/dev/null 2>&1; then
  echo "    Initializing new restic repository..."
  run_restic init
fi

tags=(--tag getafjob --tag "schema:$schema_version")
for t in "${EXTRA_TAGS[@]}"; do
  tags+=(--tag "$t")
done

if [ -n "${RESTIC_BIN:-}" ] || (command -v restic >/dev/null 2>&1 && [ "${GETAFJOB_FORCE_RESTIC_DOCKER:-0}" = "0" ]); then
  run_restic backup "${tags[@]}" "$staging_dir"
else
  run_restic backup "${tags[@]}" /backup-data
fi

echo "4/4 Verifying repository integrity..."
run_restic check --quiet

if [ "$DO_PRUNE" = "1" ]; then
  echo "    Applying retention policy (daily: $KEEP_DAILY, weekly: $KEEP_WEEKLY, monthly: $KEEP_MONTHLY)..."
  run_restic forget \
    --tag getafjob \
    --keep-daily "$KEEP_DAILY" \
    --keep-weekly "$KEEP_WEEKLY" \
    --keep-monthly "$KEEP_MONTHLY" \
    --prune --quiet
fi

# The admin system page shows the last backup; a failure here does not fail the backup itself.
backup_bytes="$(du -sk "$staging_dir" | cut -f1)"
"${compose[@]}" exec -T serve /usr/local/bin/getafjob admin backup-done -bytes "$((backup_bytes * 1024))" ||
  echo "WARNING: could not record the backup for the system page" >&2

echo "=== Backup completed successfully ==="
