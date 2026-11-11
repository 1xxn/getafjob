-- Planner queries (#36, ADR 0016).

-- name: PlanUsers :many
-- System role: active users whose autonomy level enables auto-queueing and who have daily quota > 0.
SELECT u.id
FROM users u
LEFT JOIN autonomy_settings s ON s.user_id = u.id
WHERE u.disabled_at IS NULL
  AND coalesce(s.level, 'approve') IN ('approve', 'autonomous')
  AND (sqlc.arg(unlimited)::bool OR coalesce(s.daily_limit, 5) > 0)
ORDER BY u.created_at, u.id;

-- name: ActiveApplicationsCount :one
SELECT count(*)
FROM applications
WHERE user_id = $1 AND state IN ('queued', 'running', 'submitting');

-- name: UserLastPacedAt :one
SELECT coalesce(max(at_time), '1970-01-01'::timestamptz)::timestamptz AS last_at
FROM (
  SELECT a.updated_at AS at_time FROM applications a WHERE a.user_id = $1 AND a.state IN ('queued', 'running', 'submitting')
  UNION ALL
  SELECT r.leased_at AS at_time FROM runs r WHERE r.user_id = $1
  UNION ALL
  SELECT sa.started_at AS at_time FROM submit_attempts sa WHERE sa.user_id = $1
) t;

-- name: PlannerCandidates :many
-- Jobs matching the user's criteria, ordered by score descending.
SELECT j.id, m.score
FROM matches m
JOIN jobs j ON j.id = m.job_id
WHERE m.user_id = $1
  AND m.score >= sqlc.arg(min_score)::smallint
  AND m.decision = 'match'
  AND j.status = 'open'
  AND NOT EXISTS (
    SELECT 1 FROM applications a
    WHERE a.user_id = $1
      AND a.job_id = j.id
      AND a.state NOT IN ('discovered', 'matched')
  )
ORDER BY m.score DESC, j.id ASC
LIMIT 50;
