-- name: GetAISettings :one
SELECT user_id, enabled, encrypted_api_key, spent_cents, created_at, updated_at
FROM user_ai_settings
WHERE user_id = $1;

-- name: SaveAISettings :exec
INSERT INTO user_ai_settings (user_id, enabled, encrypted_api_key, spent_cents, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $5)
ON CONFLICT (user_id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    encrypted_api_key = EXCLUDED.encrypted_api_key,
    updated_at = EXCLUDED.updated_at;

-- name: IncrementSpent :exec
UPDATE user_ai_settings
SET spent_cents = spent_cents + $2, updated_at = $3
WHERE user_id = $1;

-- name: RecordUsageEvent :exec
INSERT INTO ai_usage_events (user_id, task, model, tokens_in, tokens_out, cost_cents, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListUsageEvents :many
SELECT id, user_id, task, model, tokens_in, tokens_out, cost_cents, created_at
FROM ai_usage_events
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2;
