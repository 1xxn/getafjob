-- name: GetPreferences :one
SELECT * FROM notify_preferences
WHERE user_id = $1;

-- name: UpsertPreferences :exec
INSERT INTO notify_preferences (
    user_id, email_enabled, email_address, webpush_enabled,
    ntfy_enabled, ntfy_url, ntfy_topic, ntfy_token,
    daily_summary_enabled, daily_summary_time,
    quiet_hours_enabled, quiet_hours_start, quiet_hours_end,
    created_at, updated_at
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8,
    $9, $10,
    $11, $12, $13,
    $14, $14
)
ON CONFLICT (user_id) DO UPDATE SET
    email_enabled = EXCLUDED.email_enabled,
    email_address = EXCLUDED.email_address,
    webpush_enabled = EXCLUDED.webpush_enabled,
    ntfy_enabled = EXCLUDED.ntfy_enabled,
    ntfy_url = EXCLUDED.ntfy_url,
    ntfy_topic = EXCLUDED.ntfy_topic,
    ntfy_token = EXCLUDED.ntfy_token,
    daily_summary_enabled = EXCLUDED.daily_summary_enabled,
    daily_summary_time = EXCLUDED.daily_summary_time,
    quiet_hours_enabled = EXCLUDED.quiet_hours_enabled,
    quiet_hours_start = EXCLUDED.quiet_hours_start,
    quiet_hours_end = EXCLUDED.quiet_hours_end,
    updated_at = EXCLUDED.updated_at;

-- name: ListEnabledDailySummaryUsers :many
SELECT np.*, u.email AS user_email
FROM notify_preferences np
JOIN users u ON u.id = np.user_id
WHERE np.daily_summary_enabled = true;

-- name: UpdateLastSummaryAt :exec
UPDATE notify_preferences
SET last_summary_at = $2, updated_at = $2
WHERE user_id = $1;

-- name: GetUserEmail :one
SELECT email FROM users WHERE id = $1;

-- name: CountSubmissionsSince :one
SELECT count(*)::bigint
FROM submit_attempts
WHERE user_id = $1 AND started_at >= $2;

-- name: CountNeedsInputApplications :one
SELECT count(*)::bigint
FROM applications
WHERE user_id = $1 AND state = 'needs_input';

-- name: CountPendingSuggestions :one
SELECT count(*)::bigint
FROM tracking_suggestions
WHERE user_id = $1 AND status = 'pending';

-- name: CountPendingApprovals :one
SELECT count(*)::bigint
FROM applications
WHERE user_id = $1 AND state = 'queued';

-- name: CategoryCountsSince :many
SELECT category, count(*)::bigint AS count
FROM tracking_suggestions
WHERE user_id = $1 AND created_at >= $2
GROUP BY category;
