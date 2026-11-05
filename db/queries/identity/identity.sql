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
SELECT s.user_id, s.method, s.created_at, s.last_seen_at, s.expires_at, s.rotated_at, s.replaced_at,
       u.email, u.display_name, u.role, u.locale, u.disabled_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = $2 WHERE token_hash = $1;

-- name: DeleteSession :exec
-- Ends the session with all its rotated tokens.
DELETE FROM sessions s USING sessions t
WHERE t.token_hash = $1 AND s.family = t.family;

-- name: DeleteExpired :exec
WITH s AS (DELETE FROM sessions WHERE sessions.expires_at < $1 OR sessions.replaced_at < $1 - interval '5 minutes'),
     m AS (DELETE FROM magic_links WHERE magic_links.expires_at < $1)
DELETE FROM webauthn_challenges WHERE webauthn_challenges.expires_at < $1;

-- name: InsertAuditEvent :exec
INSERT INTO audit_events (user_id, actor, code, params) VALUES ($1, $2, $3, $4);

-- name: ReplaceSession :one
-- Only one request rotates a session; a concurrent one finds it replaced already.
UPDATE sessions SET replaced_at = sqlc.arg(at) WHERE token_hash = sqlc.arg(token_hash) AND replaced_at IS NULL
RETURNING user_id, method, created_at, expires_at, family;

-- name: CreateRotatedSession :exec
INSERT INTO sessions (token_hash, user_id, method, created_at, last_seen_at, expires_at, rotated_at, family)
VALUES ($1, $2, $3, $4, $5, $6, $5, $7);

-- name: UserSessions :many
SELECT token_hash, method, created_at, last_seen_at, expires_at FROM sessions
WHERE user_id = $1 AND replaced_at IS NULL ORDER BY last_seen_at DESC LIMIT 100;

-- name: DeleteOtherSessions :execrows
DELETE FROM sessions o WHERE o.user_id = $1
  AND o.family <> (SELECT k.family FROM sessions k WHERE k.token_hash = sqlc.arg(keep));

-- name: DeleteUserSessions :execrows
DELETE FROM sessions WHERE user_id = $1;

-- name: CreateChallenge :one
INSERT INTO webauthn_challenges (user_id, kind, data, expires_at) VALUES ($1, $2, $3, $4) RETURNING id;

-- name: TakeChallenge :one
-- A challenge is used once.
DELETE FROM webauthn_challenges
WHERE id = $1 AND kind = $2 AND expires_at > sqlc.arg(now)
RETURNING user_id, data;

-- name: UserPasskeys :many
SELECT id, name, credential, created_at, last_used_at FROM passkeys WHERE user_id = $1 ORDER BY created_at;

-- name: InsertPasskey :exec
INSERT INTO passkeys (id, user_id, name, credential, created_at) VALUES ($1, $2, $3, $4, $5);

-- name: UsePasskey :one
UPDATE passkeys SET credential = $3, last_used_at = $4 WHERE id = $1 AND user_id = $2 RETURNING user_id;

-- name: DeletePasskey :execrows
DELETE FROM passkeys WHERE id = $1 AND user_id = $2;
