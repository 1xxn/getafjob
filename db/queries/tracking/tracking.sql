-- name: GetMailbox :one
SELECT user_id, host, port, tls, username, encrypted_password, enabled, last_uid, uid_validity, last_checked_at, last_error, created_at, updated_at
FROM tracking_mailboxes WHERE user_id = $1;

-- name: SaveMailbox :exec
INSERT INTO tracking_mailboxes (user_id, host, port, tls, username, encrypted_password, enabled, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
ON CONFLICT (user_id) DO UPDATE SET
  host = EXCLUDED.host,
  port = EXCLUDED.port,
  tls = EXCLUDED.tls,
  username = EXCLUDED.username,
  encrypted_password = EXCLUDED.encrypted_password,
  enabled = EXCLUDED.enabled,
  updated_at = EXCLUDED.updated_at;

-- name: UpdateMailboxCursor :exec
UPDATE tracking_mailboxes SET
  last_uid = $2,
  uid_validity = $3,
  last_checked_at = $4,
  last_error = $5,
  updated_at = $4
WHERE user_id = $1;

-- name: ListEnabledMailboxes :many
-- System role: mailboxes that are enabled for periodic inbox checking.
SELECT user_id, host, port, tls, username, encrypted_password, enabled, last_uid, uid_validity, last_checked_at, last_error, created_at, updated_at
FROM tracking_mailboxes WHERE enabled = true ORDER BY coalesce(last_checked_at, to_timestamp(0)) ASC;

-- name: ActiveApplicationsForTracking :many
-- Return active or submitted applications for matching incoming emails.
SELECT a.id, a.user_id, a.job_id, a.state, j.company, j.title, j.match_key, coalesce(nullif(j.apply_url, ''), j.url) AS url
FROM applications a
JOIN jobs j ON j.id = a.job_id
WHERE a.user_id = $1
  AND a.state IN ('submitting', 'submitted', 'confirmed', 'interview', 'needs_input', 'submit_unknown')
ORDER BY a.updated_at DESC;

-- name: CreateSuggestion :exec
INSERT INTO tracking_suggestions (
  id, user_id, application_id, message_uid, message_id, message_from,
  message_subject, message_date, category, suggested_state, reasons, status, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'pending', $12, $12)
ON CONFLICT (user_id, application_id, message_uid) DO NOTHING;

-- name: SuggestionsByUser :many
SELECT s.id, s.user_id, s.application_id, s.message_uid, s.message_id, s.message_from,
  s.message_subject, s.message_date, s.category, s.suggested_state, s.reasons, s.status, s.created_at, s.updated_at,
  j.company, j.title, a.state AS current_state
FROM tracking_suggestions s
JOIN applications a ON a.id = s.application_id
JOIN jobs j ON j.id = a.job_id
WHERE s.user_id = $1 AND (sqlc.narg(status)::text IS NULL OR s.status = sqlc.narg(status)::text)
ORDER BY s.message_date DESC, s.created_at DESC;

-- name: SuggestionsByApplication :many
SELECT s.id, s.user_id, s.application_id, s.message_uid, s.message_id, s.message_from,
  s.message_subject, s.message_date, s.category, s.suggested_state, s.reasons, s.status, s.created_at, s.updated_at,
  j.company, j.title, a.state AS current_state
FROM tracking_suggestions s
JOIN applications a ON a.id = s.application_id
JOIN jobs j ON j.id = a.job_id
WHERE s.user_id = $1 AND s.application_id = $2
ORDER BY s.message_date DESC;

-- name: SuggestionByID :one
SELECT s.id, s.user_id, s.application_id, s.message_uid, s.message_id, s.message_from,
  s.message_subject, s.message_date, s.category, s.suggested_state, s.reasons, s.status, s.created_at, s.updated_at,
  j.company, j.title, a.state AS current_state
FROM tracking_suggestions s
JOIN applications a ON a.id = s.application_id
JOIN jobs j ON j.id = a.job_id
WHERE s.id = $1 AND s.user_id = $2;

-- name: SetSuggestionStatus :execrows
UPDATE tracking_suggestions SET status = $3, updated_at = $4
WHERE id = $1 AND user_id = $2 AND status = 'pending';
