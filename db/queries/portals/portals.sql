-- name: SavePortalSession :exec
INSERT INTO portal_sessions (user_id, portal, status, encrypted_cookies, daily_limit, last_used_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
ON CONFLICT (user_id, portal) DO UPDATE SET
  status = EXCLUDED.status,
  encrypted_cookies = EXCLUDED.encrypted_cookies,
  daily_limit = EXCLUDED.daily_limit,
  updated_at = EXCLUDED.updated_at;

-- name: GetPortalSession :one
SELECT user_id, portal, status, encrypted_cookies, daily_limit, last_used_at, created_at, updated_at
FROM portal_sessions
WHERE user_id = $1 AND portal = $2;

-- name: ListPortalSessions :many
SELECT user_id, portal, status, encrypted_cookies, daily_limit, last_used_at, created_at, updated_at
FROM portal_sessions
WHERE user_id = $1
ORDER BY portal;

-- name: UpdatePortalStatus :exec
UPDATE portal_sessions
SET status = $3, updated_at = $4
WHERE user_id = $1 AND portal = $2;

-- name: TouchPortalLastUsed :exec
UPDATE portal_sessions
SET last_used_at = $3, updated_at = $3
WHERE user_id = $1 AND portal = $2;

-- name: DeletePortalSession :exec
DELETE FROM portal_sessions
WHERE user_id = $1 AND portal = $2;

-- name: PortalSubmitsSince :one
SELECT count(*)
FROM submit_attempts sa
JOIN applications a ON a.id = sa.application_id
JOIN jobs j ON j.id = a.job_id
WHERE sa.user_id = $1
  AND (j.source = $2 OR j.source LIKE ($2 || ':%'))
  AND sa.started_at >= $3;

-- name: ActivePortalRuns :one
SELECT count(*)
FROM applications a
JOIN jobs j ON j.id = a.job_id
WHERE a.user_id = $1
  AND (j.source = $2 OR j.source LIKE ($2 || ':%'))
  AND a.state IN ('running', 'needs_input');
