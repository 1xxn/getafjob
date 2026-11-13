#!/usr/bin/env bash
# Restore getafjob from an encrypted restic backup into PostgreSQL and the blob store.
set -euo pipefail

export MSYS_NO_PATHCONV=1

RESTIC_IMAGE="${GETAFJOB_RESTIC_IMAGE:-restic/restic:0.17.3@sha256:8f5a62b422a2cb1277ea0dd6e826fe1acf649e5b9f02d60e5268d5fd1976255a}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/compose.yaml}"
SECRETS_DIR="${GETAFJOB_SECRETS_DIR:-deploy/secrets}"
BACKUP_DIR="${GETAFJOB_BACKUP_DIR:-deploy/backups}"
RESTIC_REPOSITORY="${RESTIC_REPOSITORY:-$BACKUP_DIR/restic}"
SNAPSHOT_ID="${RESTIC_SNAPSHOT_ID:-latest}"
RESTORE_SECRETS="${RESTORE_SECRETS:-0}"
ASSUME_YES="${RESTORE_YES:-0}"

usage() {
  cat <<EOF
Usage: $0 [options] [snapshot-id]

Restore getafjob PostgreSQL database and blob store from an encrypted restic backup.

Options:
  --snapshot <id>         Snapshot ID to restore (default: latest)
  --restore-secrets       Restore secrets if present in the snapshot
  --yes                   Do not ask before replacing the database and the blob store
  -h, --help              Show this help

Environment variables:
  RESTIC_REPOSITORY       Restic repository URL or local directory (default: deploy/backups/restic)
  RESTIC_PASSWORD         Encryption password for restic
  RESTIC_PASSWORD_FILE    Path to restic password file (default: deploy/secrets/backup_password if present)
  GETAFJOB_SECRETS_DIR    Directory containing secrets (default: deploy/secrets)
  COMPOSE_FILE            Docker compose file (default: deploy/compose.yaml)
EOF
  exit 0
}

while [ $# -gt 0 ]; do
  case "$1" in
    --snapshot) SNAPSHOT_ID="$2"; shift 2 ;;
    --restore-secrets) RESTORE_SECRETS=1; shift ;;
    --yes) ASSUME_YES=1; shift ;;
    -h|--help) usage ;;
    -*) echo "Unknown option: $1" >&2; usage ;;
    *) SNAPSHOT_ID="$1"; shift ;;
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

run_restic() {
  if [ -n "${RESTIC_BIN:-}" ] || (command -v restic >/dev/null 2>&1 && [ "${GETAFJOB_FORCE_RESTIC_DOCKER:-0}" = "0" ]); then
    RESTIC_PASSWORD="$RESTIC_PASSWORD" RESTIC_REPOSITORY="$RESTIC_REPOSITORY" "${RESTIC_BIN:-restic}" "$@"
  else
    local docker_args=(docker run --rm -i)
    local repo_arg="$RESTIC_REPOSITORY"

    if is_remote_repo; then
      for var in AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_DEFAULT_REGION AWS_ENDPOINT \
                 B2_ACCOUNT_ID B2_ACCOUNT_KEY AZURE_ACCOUNT_NAME AZURE_ACCOUNT_KEY; do
        if [ -n "${!var:-}" ]; then
          export "${var?}"
          docker_args+=(-e "$var") # by name: the value never shows in the process list
        fi
      done
    else
      local abs_repo
      abs_repo="$(cd "$RESTIC_REPOSITORY" && pwd)"
      docker_args+=(-v "$abs_repo:/repo")
      repo_arg="/repo"
    fi

    if [ -n "${RESTIC_RESTORE_DIR:-}" ] && [ -d "$RESTIC_RESTORE_DIR" ]; then
      local abs_restore
      abs_restore="$(cd "$RESTIC_RESTORE_DIR" && pwd)"
      docker_args+=(-v "$abs_restore:/restore")
    fi

    docker_args+=(
      -e "RESTIC_REPOSITORY=$repo_arg"
      -e RESTIC_PASSWORD
      "$RESTIC_IMAGE"
    )
    RESTIC_PASSWORD="$RESTIC_PASSWORD" "${docker_args[@]}" "$@"
  fi
}

# A restore replaces all data of the project: ask first, unless --yes says it was meant.
if [ "$ASSUME_YES" != "1" ]; then
  if [ ! -t 0 ]; then
    echo "ERROR: a restore replaces the database and the blob store of '$COMPOSE_PROJECT'; pass --yes to confirm." >&2
    exit 1
  fi
  read -r -p "Replace all data of '$COMPOSE_PROJECT' with snapshot $SNAPSHOT_ID? Type the project name: " answer
  if [ "$answer" != "$COMPOSE_PROJECT" ]; then
    echo "Aborted." >&2
    exit 1
  fi
fi

echo "=== Starting getafjob restore ==="
echo "Repository: $RESTIC_REPOSITORY"
echo "Snapshot:   $SNAPSHOT_ID"

# Create temporary restore directory in workspace tmp
mkdir -p tmp
staging_dir="$(mktemp -d tmp/restore-XXXXXX)"
RESTIC_RESTORE_DIR="$staging_dir"
cleanup() {
  rm -rf "$staging_dir" 2>/dev/null || docker run --rm -v "$(cd "$(dirname "$staging_dir")" && pwd):/cleanup" alpine rm -rf "/cleanup/$(basename "$staging_dir")" 2>/dev/null || true
}
trap cleanup EXIT

echo "1/5 Extracting snapshot from restic..."
if [ -n "${RESTIC_BIN:-}" ] || (command -v restic >/dev/null 2>&1 && [ "${GETAFJOB_FORCE_RESTIC_DOCKER:-0}" = "0" ]); then
  run_restic restore "$SNAPSHOT_ID" --target "$staging_dir"
else
  run_restic restore "$SNAPSHOT_ID" --target /restore
fi

# Locate restored items regardless of source path hierarchy
dump_file="$(find "$staging_dir" -type f -name "postgres.dump" | head -n1)"
blobs_dir="$(find "$staging_dir" -type d -name "blobs" | head -n1)"
manifest_file="$(find "$staging_dir" -type f -name "manifest.json" | head -n1)"
secrets_dir_restored="$(find "$staging_dir" -type d -name "secrets" | head -n1)"

if [ -z "$dump_file" ] || [ ! -f "$dump_file" ]; then
  echo "ERROR: Restored snapshot does not contain postgres.dump" >&2
  exit 1
fi

if [ -n "$manifest_file" ] && [ -f "$manifest_file" ]; then
  echo "    Backup manifest: $(cat "$manifest_file")"
fi

echo "2/5 Stopping application services..."
"${compose[@]}" stop serve worker runner >/dev/null 2>&1 || true

echo "3/5 Ensuring PostgreSQL is ready..."
"${compose[@]}" up -d postgres
for _ in $(seq 1 30); do
  if "${compose[@]}" exec -T postgres pg_isready -U getafjob -d getafjob >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "4/5 Restoring PostgreSQL database..."
# Terminate other sessions and wipe schema public
"${compose[@]}" exec -T postgres psql -U getafjob -d template1 -q -c \
  "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = 'getafjob' AND pid <> pg_backend_pid();" >/dev/null 2>&1 || true
"${compose[@]}" exec -T postgres psql -U getafjob -d getafjob -q -c \
  "DROP SCHEMA public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO getafjob; GRANT USAGE ON SCHEMA public TO getafjob_app;"

# Copy dump into container and restore
restore_container_path="/var/lib/postgresql/getafjob_restore.dump"
"${compose[@]}" cp "$dump_file" "postgres:$restore_container_path"
"${compose[@]}" exec -T postgres pg_restore -U getafjob -d getafjob "$restore_container_path"
"${compose[@]}" exec -T postgres rm -f "$restore_container_path"

echo "5/5 Restoring blob volume..."
if [ -n "$blobs_dir" ] && [ -d "$blobs_dir" ]; then
  docker run --rm \
    -v "${COMPOSE_PROJECT}_blobs:/dst" \
    -v "$(cd "$blobs_dir" && pwd):/src:ro" \
    alpine sh -c "find /dst -mindepth 1 -delete && cp -a /src/. /dst/ && chown -R 65532:65532 /dst"
fi

if [ "$RESTORE_SECRETS" = "1" ] && [ -n "$secrets_dir_restored" ] && [ -d "$secrets_dir_restored" ]; then
  echo "    Restoring secrets to $SECRETS_DIR..."
  mkdir -p "$SECRETS_DIR"
  cp -a "$secrets_dir_restored/." "$SECRETS_DIR/"
  chmod -R 0600 "$SECRETS_DIR"/* 2>/dev/null || true
fi

echo "Starting all services and awaiting health..."
"${compose[@]}" up -d --wait

echo "=== Restore completed successfully ==="
