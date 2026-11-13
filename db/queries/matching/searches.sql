-- name: SavedSearches :many
SELECT id, name, query, alert, checked_at FROM saved_searches WHERE user_id = $1 ORDER BY lower(name), id;

-- name: CreateSavedSearch :one
INSERT INTO saved_searches (user_id, name, query, alert, checked_at) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id, name) DO UPDATE SET query = EXCLUDED.query
RETURNING id;

-- name: RenameSavedSearch :execrows
UPDATE saved_searches SET name = $3 WHERE user_id = $1 AND id = $2;

-- name: SetSavedSearchAlert :execrows
-- Turning the alert on starts counting new jobs from now.
UPDATE saved_searches SET alert = $3, checked_at = CASE WHEN $3 AND NOT alert THEN $4 ELSE checked_at END
WHERE user_id = $1 AND id = $2;

-- name: DeleteSavedSearch :execrows
DELETE FROM saved_searches WHERE user_id = $1 AND id = $2;

-- name: DueSearchAlerts :many
-- System role: saved searches with an alert that was not checked within the interval.
SELECT id, user_id, name, query, checked_at FROM saved_searches
WHERE alert AND checked_at <= $1 ORDER BY checked_at, id LIMIT 500;

-- name: MarkSearchChecked :exec
UPDATE saved_searches SET checked_at = $3 WHERE user_id = $1 AND id = $2;

-- name: MatchedUntil :one
-- When the user's matches were last computed; jobs first seen up to then have a result.
SELECT coalesce(max(computed_at), 'epoch'::timestamptz)::timestamptz FROM matches WHERE user_id = $1;
