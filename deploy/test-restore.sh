#!/usr/bin/env bash
# Automated rehearsal: creates test data, backs up via restic, wipes database and blobs,
# restores from backup and proves recovery with integrity checks and the smoke test.
set -euo pipefail

export MSYS_NO_PATHCONV=1
compose=(docker compose -f deploy/compose.yaml)

mkdir -p tmp
test_dir="$(mktemp -d tmp/test-restore-XXXXXX)"
test_repo="$test_dir/repo"
test_password="rehearsal-password-$(date +%s)"

cleanup() {
  rm -rf "$test_dir" 2>/dev/null || docker run --rm -v "$(cd "$(dirname "$test_dir")" && pwd):/cleanup" alpine rm -rf "/cleanup/$(basename "$test_dir")" 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Starting automated restore rehearsal ==="

# Detect project name
COMPOSE_PROJECT="$("${compose[@]}" config --format json 2>/dev/null | grep -o '"name": "[^"]*"' | head -n1 | cut -d'"' -f4 || echo "getafjob")"
[ -z "$COMPOSE_PROJECT" ] && COMPOSE_PROJECT="getafjob"

# 1. Ensure stack is healthy
echo "1. Checking compose stack..."
"${compose[@]}" up -d --wait

# 2. Seed demo data if database is empty
job_count="$("${compose[@]}" exec -T postgres psql -U getafjob -d getafjob -t -c "SELECT count(*) FROM jobs;" 2>/dev/null | tr -d '[:space:]' || echo "0")"
if [ "$job_count" = "0" ] || [ -z "$job_count" ]; then
  echo "Seeding demo data for test..."
  "${compose[@]}" exec -T serve /usr/local/bin/getafjob admin demo -jobs 20 >/dev/null
fi

pre_users="$("${compose[@]}" exec -T postgres psql -U getafjob -d getafjob -t -c "SELECT count(*) FROM users;" | tr -d '[:space:]')"
pre_jobs="$("${compose[@]}" exec -T postgres psql -U getafjob -d getafjob -t -c "SELECT count(*) FROM jobs;" | tr -d '[:space:]')"
pre_matches="$("${compose[@]}" exec -T postgres psql -U getafjob -d getafjob -t -c "SELECT count(*) FROM matches;" | tr -d '[:space:]')"

echo "Pre-backup database records: users=$pre_users, jobs=$pre_jobs, matches=$pre_matches"

# 3. Write a canary file into the blob store
canary_token="canary-$(date +%s)-$RANDOM"
docker run --rm -v "${COMPOSE_PROJECT}_blobs:/blobs" alpine sh -c "echo '$canary_token' > /blobs/rehearsal-canary.txt"
echo "Injected blob canary: $canary_token"

# 4. Take restic backup into temporary repository
echo "2. Running backup.sh into temporary repository..."
RESTIC_REPOSITORY="$test_repo" \
RESTIC_PASSWORD="$test_password" \
deploy/backup.sh --tag "rehearsal" --no-prune

# 5. Simulate total loss: wipe database and blobs
echo "3. Simulating data disaster (wiping database and blobs)..."
"${compose[@]}" stop serve worker runner >/dev/null
"${compose[@]}" exec -T postgres psql -U getafjob -d getafjob -q -c \
  "DROP SCHEMA public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO getafjob; GRANT USAGE ON SCHEMA public TO getafjob_app;"
docker run --rm -v "${COMPOSE_PROJECT}_blobs:/blobs" alpine sh -c "rm -rf /blobs/*"

# Verify wipe
wiped_tables="$("${compose[@]}" exec -T postgres psql -U getafjob -d getafjob -t -c "SELECT count(*) FROM information_schema.tables WHERE table_schema='public';" | tr -d '[:space:]')"
wiped_blobs="$(docker run --rm -v "${COMPOSE_PROJECT}_blobs:/blobs:ro" alpine sh -c "ls -1 /blobs | wc -l" | tr -d '[:space:]')"

if [ "$wiped_tables" != "0" ] || [ "$wiped_blobs" != "0" ]; then
  echo "ERROR: Disaster simulation failed; database or blobs not empty." >&2
  exit 1
fi
echo "Disaster simulated: tables=$wiped_tables, blobs=$wiped_blobs"

# 6. Restore from backup
echo "4. Running restore.sh..."
RESTIC_REPOSITORY="$test_repo" \
RESTIC_PASSWORD="$test_password" \
deploy/restore.sh latest

# 7. Verify restored state
echo "5. Verifying restored data integrity..."
post_users="$("${compose[@]}" exec -T postgres psql -U getafjob -d getafjob -t -c "SELECT count(*) FROM users;" | tr -d '[:space:]')"
post_jobs="$("${compose[@]}" exec -T postgres psql -U getafjob -d getafjob -t -c "SELECT count(*) FROM jobs;" | tr -d '[:space:]')"
post_matches="$("${compose[@]}" exec -T postgres psql -U getafjob -d getafjob -t -c "SELECT count(*) FROM matches;" | tr -d '[:space:]')"

echo "Post-restore database records: users=$post_users, jobs=$post_jobs, matches=$post_matches"

if [ "$pre_users" != "$post_users" ]; then
  echo "FAIL: User count mismatch (pre: $pre_users, post: $post_users)" >&2
  exit 1
fi

if [ "$pre_jobs" != "$post_jobs" ]; then
  echo "FAIL: Job count mismatch (pre: $pre_jobs, post: $post_jobs)" >&2
  exit 1
fi

if [ "$pre_matches" != "$post_matches" ]; then
  echo "FAIL: Match count mismatch (pre: $pre_matches, post: $post_matches)" >&2
  exit 1
fi

restored_canary="$(docker run --rm -v "${COMPOSE_PROJECT}_blobs:/blobs:ro" alpine cat /blobs/rehearsal-canary.txt | tr -d '[:space:]')"
if [ "$restored_canary" != "$canary_token" ]; then
  echo "FAIL: Blob canary mismatch (want $canary_token, got $restored_canary)" >&2
  exit 1
fi
echo "Blob canary verified: $restored_canary"

# Clean up canary
docker run --rm -v "${COMPOSE_PROJECT}_blobs:/blobs" alpine rm -f /blobs/rehearsal-canary.txt

# 8. Run smoke test on the restored stack
echo "6. Running smoke test on restored stack..."
deploy/smoke.sh

echo "=== RESTORE REHEARSAL PASSED: 100% OK ==="
