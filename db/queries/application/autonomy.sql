-- name: GetSettings :one
SELECT level, min_score, daily_limit, runner_choice, easy_apply_enabled, easy_apply_terms_accepted_at
FROM autonomy_settings WHERE user_id = $1;

-- name: SaveSettings :exec
INSERT INTO autonomy_settings (user_id, level, min_score, daily_limit, runner_choice, easy_apply_enabled, easy_apply_terms_accepted_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (user_id) DO UPDATE SET
  level = EXCLUDED.level,
  min_score = EXCLUDED.min_score,
  daily_limit = EXCLUDED.daily_limit,
  runner_choice = EXCLUDED.runner_choice,
  easy_apply_enabled = EXCLUDED.easy_apply_enabled,
  easy_apply_terms_accepted_at = EXCLUDED.easy_apply_terms_accepted_at,
  updated_at = EXCLUDED.updated_at;

-- name: SaveHomeRunnerToken :exec
UPDATE autonomy_settings SET home_runner_token_hash = $2, updated_at = $3 WHERE user_id = $1;

-- name: UserByHomeRunnerToken :one
SELECT user_id FROM autonomy_settings WHERE home_runner_token_hash = $1;

-- name: AskApproval :exec
-- Puts an application on the approval list; a former approval no longer counts.
UPDATE applications SET approval_asked_at = sqlc.arg(at), approval_reasons = sqlc.arg(reasons)::text[], approved_at = NULL
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: ApproveApplication :execrows
UPDATE applications SET approved_at = sqlc.arg(at), version = version + 1, updated_at = sqlc.arg(at)
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND version = sqlc.arg(version)
  AND state IN ('matched', 'queued', 'running', 'needs_input', 'parked', 'failed');

-- name: Approvals :many
SELECT a.id, a.job_id, a.state, a.approval_reasons, a.approval_asked_at::timestamptz AS asked_at, j.title, j.company,
  coalesce(m.score, 0)::smallint AS score, a.cv_document_id
FROM applications a JOIN jobs j ON j.id = a.job_id
LEFT JOIN matches m ON m.user_id = a.user_id AND m.job_id = a.job_id
WHERE a.user_id = $1 AND a.approval_asked_at IS NOT NULL AND a.approved_at IS NULL
  AND a.state IN ('needs_input', 'parked')
ORDER BY a.approval_asked_at, a.id
LIMIT 200;

-- name: MatchScore :one
SELECT score FROM matches WHERE user_id = $1 AND job_id = $2;

-- name: RunCheckpoints :many
SELECT step, page, data FROM run_checkpoints WHERE run_id = $1 AND user_id = $2 ORDER BY id;

-- name: SubmitsSince :one
SELECT count(*) FROM submit_attempts WHERE user_id = $1 AND started_at >= $2;

-- name: RunAdapter :one
SELECT adapter FROM runs WHERE id = $1 AND user_id = $2;

-- name: SetRunAdapter :exec
UPDATE runs SET adapter = $3 WHERE id = $1 AND user_id = $2;

-- name: AdapterRuns :one
-- System role: finished runs of an adapter since a time (all users), and how many of them were submitted. Runs that
-- a guard stopped before the form say nothing about the adapter.
SELECT count(*) AS runs, count(*) FILTER (WHERE outcome = 'submitted') AS submitted
FROM runs WHERE adapter = $1 AND finished_at IS NOT NULL AND finished_at >= $2 AND outcome <> 'guard';

-- name: NavCounts :one
-- needs_input also counts runs parked to wait for answers (the runner freed the browser); the page lists both.
SELECT
  count(*) FILTER (WHERE (approval_asked_at IS NULL OR approved_at IS NOT NULL) AND (state = 'needs_input'
    OR (state = 'parked' AND (SELECT e.detail->>'reason' FROM application_events e
      WHERE e.application_id = applications.id AND e.to_state = 'parked' ORDER BY e.id DESC LIMIT 1)
      LIKE 'waiting for your answers%')))::int AS needs_input,
  count(*) FILTER (WHERE approval_asked_at IS NOT NULL AND approved_at IS NULL AND state IN ('needs_input', 'parked'))::int AS approvals,
  count(*) FILTER (WHERE state IN ('running', 'submitting'))::int AS running
FROM applications WHERE applications.user_id = $1;
