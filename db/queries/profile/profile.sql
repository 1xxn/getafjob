-- name: LoadProfile :one
SELECT sealed, preferences, version, updated_at FROM profiles WHERE user_id = $1;

-- name: SaveProfile :execrows
-- Optimistic locking: the row must still have the version the editor loaded (0: no row yet).
INSERT INTO profiles (user_id, sealed, version, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(sealed), 1, sqlc.arg(updated_at))
ON CONFLICT (user_id) DO UPDATE SET sealed = EXCLUDED.sealed, version = profiles.version + 1, updated_at = EXCLUDED.updated_at
WHERE profiles.version = sqlc.arg(version)::bigint;

-- name: SavePreferences :exec
INSERT INTO profiles (user_id, preferences, updated_at) VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE SET preferences = EXCLUDED.preferences, updated_at = EXCLUDED.updated_at;
