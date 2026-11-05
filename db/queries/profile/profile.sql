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

-- name: InsertDocument :exec
INSERT INTO documents (id, user_id, kind, sealed_name, mime, size, label, language, focus, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListDocuments :many
SELECT id, kind, sealed_name, mime, size, label, language, focus, created_at FROM documents
WHERE user_id = $1 ORDER BY created_at, id;

-- name: GetDocument :one
SELECT id, kind, sealed_name, mime, size, label, language, focus, created_at FROM documents WHERE id = $1 AND user_id = $2;

-- name: CountDocuments :one
SELECT count(*) FROM documents WHERE user_id = $1;

-- name: UpdateVariant :execrows
UPDATE documents SET label = $3, language = $4, focus = $5 WHERE id = $1 AND user_id = $2 AND kind = 'cv';

-- name: DeleteDocument :execrows
DELETE FROM documents WHERE id = $1 AND user_id = $2;

-- name: ListItems :many
SELECT id, sealed, updated_at FROM profile_items WHERE user_id = $1 AND kind = $2 ORDER BY id;

-- name: CountItems :one
SELECT count(*) FROM profile_items WHERE user_id = $1 AND kind = $2;

-- name: InsertItem :exec
INSERT INTO profile_items (id, user_id, kind, sealed, updated_at) VALUES ($1, $2, $3, $4, $5);

-- name: UpdateItem :execrows
UPDATE profile_items SET sealed = $4, updated_at = $5 WHERE id = $1 AND user_id = $2 AND kind = $3;

-- name: DeleteItem :execrows
DELETE FROM profile_items WHERE id = $1 AND user_id = $2 AND kind = $3;
