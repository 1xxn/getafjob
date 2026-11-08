-- name: CreatePairingCode :exec
INSERT INTO assist_pairings (id, user_id, code_hash, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetPairingCode :one
SELECT id, user_id, code_hash, created_at, expires_at
FROM assist_pairings
WHERE code_hash = $1 AND expires_at > $2;

-- name: DeletePairingCode :exec
DELETE FROM assist_pairings
WHERE id = $1;

-- name: CleanupExpiredPairings :exec
DELETE FROM assist_pairings
WHERE expires_at <= $1;

-- name: CreateExtensionToken :exec
INSERT INTO assist_tokens (id, user_id, token_hash, client_name, created_at, expires_at, last_used_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetExtensionToken :one
SELECT id, user_id, token_hash, client_name, created_at, expires_at, last_used_at
FROM assist_tokens
WHERE token_hash = $1 AND expires_at > $2;

-- name: TouchExtensionToken :exec
UPDATE assist_tokens
SET last_used_at = $2
WHERE token_hash = $1;

-- name: ListExtensionTokens :many
SELECT id, user_id, client_name, created_at, expires_at, last_used_at
FROM assist_tokens
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: RevokeExtensionToken :exec
DELETE FROM assist_tokens
WHERE id = $1 AND user_id = $2;
