#!/usr/bin/env bash
# Automated test rehearsal for deploy workflow: deployment, health check, and automatic rollback.
#
# Scenario:
#   1. Deploy version v-test-1 into an isolated project (getafjob-test-deploy on port 18443).
#   2. Verify health check passes and deployment state records v-test-1.
#   3. Trigger a deployment of v-test-2 configured to fail health check.
#   4. Verify that deploy.sh detects failure and automatically rolls back to v-test-1.
#   5. Verify that v-test-1 is restored to healthy state and state file is intact.
#   6. Verify that manual rollback works.
#   7. Clean teardown.
set -euo pipefail

export MSYS_NO_PATHCONV=1

TEST_PROJECT="getafjob-test-deploy"
TEST_PORT="18443"
HEALTH_URL="https://localhost:${TEST_PORT}/healthz"

echo "========================================================================"
echo " Starting Deploy & Rollback Rehearsal"
echo " Project: $TEST_PROJECT | Port: $TEST_PORT"
echo "========================================================================"

# Ensure dev secrets exist
if [ ! -f deploy/secrets/db_password ] || [ ! -f deploy/secrets/master_key ]; then
  echo "Generating dev secrets for rehearsal..."
  go tool task dev:secrets
fi

# Create temporary workspace directory
mkdir -p tmp
test_dir="$(mktemp -d tmp/deploy-test-XXXXXX)"
state_dir="$test_dir/state"
override_file="$test_dir/override.yaml"
mkdir -p "$state_dir"

cat <<EOF > "$override_file"
services:
  caddy:
    ports: !override
      - "127.0.0.1:${TEST_PORT}:443"
EOF

cleanup() {
  echo ""
  echo "==> Cleaning up test stack and resources..."
  docker compose -f deploy/compose.yaml -f "$override_file" -p "$TEST_PROJECT" down -v >/dev/null 2>&1 || true
  rm -rf "$test_dir" 2>/dev/null || docker run --rm -v "$(cd "$(dirname "$test_dir")" && pwd):/cleanup" alpine rm -rf "/cleanup/$(basename "$test_dir")" 2>/dev/null || true
  echo "==> Cleanup complete."
}
trap cleanup EXIT

# 1. Initial deployment: v-test-1
echo ""
echo "--- TEST 1: Initial deployment of v-test-1 ---"
if ! deploy/deploy.sh \
  --skip-pull \
  --skip-cosign \
  --skip-smoke \
  --project "$TEST_PROJECT" \
  --files "-f deploy/compose.yaml -f $override_file" \
  --state-dir "$state_dir" \
  --health-url "$HEALTH_URL" \
  --timeout 120 \
  "v-test-1"; then
  echo "FAILED: Initial deployment of v-test-1 returned non-zero exit code!" >&2
  exit 1
fi

recorded_version="$(cat "$state_dir/${TEST_PROJECT}.version" 2>/dev/null | tr -d '\r\n')"
if [ "$recorded_version" != "v-test-1" ]; then
  echo "FAILED: Expected state to record 'v-test-1', got '$recorded_version'!" >&2
  exit 1
fi

if ! curl -fsSk "$HEALTH_URL" >/dev/null 2>&1; then
  echo "FAILED: Health check failed after initial deployment!" >&2
  exit 1
fi
echo "PASSED: Initial deployment of v-test-1 verified healthy."

# 2. Deployment of v-test-2 with simulated health failure -> must trigger automatic rollback
echo ""
echo "--- TEST 2: Deployment with simulated failure -> Automatic Rollback ---"
# We simulate health check failure by pointing --health-url to a non-existent endpoint with low timeout
failing_url="https://localhost:${TEST_PORT}/failing-endpoint-nonexistent"
set +e
deploy/deploy.sh \
  --skip-pull \
  --skip-cosign \
  --skip-smoke \
  --project "$TEST_PROJECT" \
  --files "-f deploy/compose.yaml -f $override_file" \
  --state-dir "$state_dir" \
  --health-url "$failing_url" \
  --timeout 4 \
  "v-test-2"
deploy_exit_code=$?
set -e

if [ "$deploy_exit_code" -ne 1 ]; then
  echo "FAILED: Expected deploy.sh to exit with code 1 after rollback, got $deploy_exit_code!" >&2
  exit 1
fi

# Verify state was NOT updated to v-test-2
state_after_rollback="$(cat "$state_dir/${TEST_PROJECT}.version" 2>/dev/null | tr -d '\r\n')"
if [ "$state_after_rollback" != "v-test-1" ]; then
  echo "FAILED: Expected state file to remain 'v-test-1', got '$state_after_rollback'!" >&2
  exit 1
fi

# Verify that the rolled-back stack is healthy
if ! curl -fsSk "$HEALTH_URL" >/dev/null 2>&1; then
  echo "FAILED: Health check failed for rolled-back stack (v-test-1)!" >&2
  exit 1
fi
echo "PASSED: Automatic rollback succeeded and restored v-test-1 to healthy state."

# 3. Manual rollback test
echo ""
echo "--- TEST 3: Manual rollback ---"
if ! deploy/rollback.sh \
  --skip-pull \
  --skip-cosign \
  --skip-smoke \
  --project "$TEST_PROJECT" \
  --files "-f deploy/compose.yaml -f $override_file" \
  --state-dir "$state_dir" \
  --health-url "$HEALTH_URL" \
  --timeout 30 \
  "v-test-1"; then
  echo "FAILED: Manual rollback returned non-zero exit code!" >&2
  exit 1
fi
echo "PASSED: Manual rollback command succeeded."

echo ""
echo "========================================================================"
echo " ALL DEPLOYMENT & ROLLBACK REHEARSAL TESTS PASSED!"
echo "========================================================================"
