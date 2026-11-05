-- name: CreateUser :one
INSERT INTO users (email, display_name, role, locale)
VALUES ($1, $2, $3, $4)
RETURNING id, email, display_name, role, locale, disabled_at;

-- name: GetUserByEmail :one
SELECT id, email, display_name, role, locale, disabled_at FROM users WHERE email = $1;

-- name: CreateInvitation :exec
INSERT INTO invitations (email, role, token_hash, created_by, expires_at) VALUES ($1, $2, $3, $4, $5);

-- name: GetInvitationByHash :one
SELECT id, email, role, expires_at, accepted_at FROM invitations WHERE token_hash = $1;

-- name: MarkInvitationAccepted :execrows
UPDATE invitations SET accepted_at = $2, accepted_user = $3 WHERE id = $1 AND accepted_at IS NULL;

-- name: CreateMagicLink :exec
INSERT INTO magic_links (token_hash, user_id, created_at, expires_at) VALUES ($1, $2, $3, $4);

-- name: CountMagicLinksSince :one
SELECT count(*) FROM magic_links WHERE user_id = $1 AND created_at > $2;

-- name: UseMagicLink :one
UPDATE magic_links SET used_at = $2
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2
RETURNING user_id;

-- name: GetUser :one
SELECT id, email, display_name, role, locale, disabled_at FROM users WHERE id = $1;

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, method, created_at, last_seen_at, expires_at)
VALUES ($1, $2, $3, $4, $4, $5);

-- name: GetSession :one
SELECT s.user_id, s.method, s.created_at, s.last_seen_at, s.expires_at,
       u.email, u.display_name, u.role, u.locale, u.disabled_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = $2 WHERE token_hash = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteExpired :exec
WITH s AS (DELETE FROM sessions WHERE sessions.expires_at < $1),
     m AS (DELETE FROM magic_links WHERE magic_links.expires_at < $1)
DELETE FROM webauthn_challenges WHERE webauthn_challenges.expires_at < $1;

-- name: InsertAuditEvent :exec
INSERT INTO audit_events (user_id, actor, code, params) VALUES ($1, $2, $3, $4);
