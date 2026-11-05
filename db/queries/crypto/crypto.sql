-- name: DataKey :one
SELECT master_key_id, wrapped FROM user_keys WHERE user_id = $1;

-- name: InsertDataKey :exec
-- A key created meanwhile by another process wins; the caller reads it again.
INSERT INTO user_keys (user_id, master_key_id, wrapped, created_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO NOTHING;

-- name: DeleteDataKey :execrows
DELETE FROM user_keys WHERE user_id = $1;

-- name: KeysToRewrap :many
SELECT user_id, master_key_id, wrapped FROM user_keys WHERE master_key_id <> sqlc.arg(current_id) ORDER BY user_id
LIMIT sqlc.arg(max_rows);

-- name: RewrapDataKey :execrows
UPDATE user_keys SET master_key_id = sqlc.arg(new_id), wrapped = sqlc.arg(wrapped), rotated_at = sqlc.arg(at)
WHERE user_id = sqlc.arg(user_id) AND master_key_id = sqlc.arg(old_id);
