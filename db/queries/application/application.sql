-- name: CreateApplication :one
INSERT INTO applications (user_id, job_id, state, created_at, updated_at) VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (user_id, job_id) DO UPDATE SET user_id = EXCLUDED.user_id
RETURNING id;

-- name: GetApplication :one
SELECT id, job_id, state, version, updated_at FROM applications WHERE id = $1 AND user_id = $2;

-- name: SetState :execrows
UPDATE applications SET state = sqlc.arg(to_state), version = version + 1, updated_at = sqlc.arg(at),
  handoff_until = sqlc.narg(handoff_until), handoff_warned = false
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND state = sqlc.arg(from_state) AND version = sqlc.arg(version);

-- name: InsertEvent :exec
INSERT INTO application_events (application_id, user_id, event, from_state, to_state, actor, detail, at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: InsertSubmitAttempt :exec
INSERT INTO submit_attempts (application_id, user_id, started_at) VALUES ($1, $2, $3);

-- name: FinishSubmitAttempt :exec
UPDATE submit_attempts SET outcome = $3, finished_at = $4 WHERE application_id = $1 AND user_id = $2;

-- name: Events :many
SELECT event, from_state, to_state, actor, detail, at FROM application_events
WHERE application_id = $1 AND user_id = $2 ORDER BY id;

-- name: SameJobStates :many
-- The user's applications for the same job reached through other sources (same match key).
SELECT a.state FROM applications a JOIN jobs j ON j.id = a.job_id
WHERE a.user_id = sqlc.arg(user_id) AND a.job_id <> sqlc.arg(job_id)
  AND j.match_key = (SELECT x.match_key FROM jobs x WHERE x.id = sqlc.arg(job_id));

-- name: CompanySubmits :many
SELECT s.started_at FROM submit_attempts s JOIN applications a ON a.id = s.application_id JOIN jobs j ON j.id = a.job_id
WHERE a.user_id = sqlc.arg(user_id) AND lower(j.company) = lower((SELECT x.company FROM jobs x WHERE x.id = sqlc.arg(job_id)));

-- name: HardSkipped :one
-- Whether matching filtered the job out for the user (a hard reason such as an excluded title).
SELECT EXISTS (SELECT 1 FROM matches WHERE user_id = $1 AND job_id = $2 AND decision = 'skip'
  AND reasons @> '[{"hard": true}]')::boolean;

-- name: StaleSubmitting :many
-- System role: applications that have been submitting for too long (the run died).
SELECT id, user_id FROM applications WHERE state = 'submitting' AND updated_at < $1 ORDER BY updated_at LIMIT 500;

-- name: HandoffsDue :many
-- System role: waiting applications whose handoff window ends before the given time.
SELECT id, user_id, handoff_until, handoff_warned FROM applications
WHERE state = 'needs_input' AND handoff_until IS NOT NULL AND handoff_until < $1 ORDER BY handoff_until LIMIT 500;

-- name: MarkHandoffWarned :execrows
UPDATE applications SET handoff_warned = true WHERE id = $1 AND user_id = $2 AND state = 'needs_input' AND NOT handoff_warned;

-- name: LeaseCandidate :one
-- System role: the oldest queued application whose host has no active run and no lease within the gap, and that
-- has no unfinished run itself.
SELECT a.id, a.user_id, a.job_id,
  lower(coalesce(substring(coalesce(nullif(j.apply_url, ''), j.url) from '^[a-zA-Z]+://([^/:?#]+)'), ''))::text AS host
FROM applications a JOIN jobs j ON j.id = a.job_id
WHERE a.state = 'queued'
  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.application_id = a.id AND r.finished_at IS NULL AND r.expires_at > sqlc.arg(now)::timestamptz)
  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.host = lower(coalesce(substring(coalesce(nullif(j.apply_url, ''), j.url) from '^[a-zA-Z]+://([^/:?#]+)'), ''))
    AND ((r.finished_at IS NULL AND r.expires_at > sqlc.arg(now)::timestamptz) OR r.leased_at > sqlc.arg(gap_start)::timestamptz))
ORDER BY a.updated_at, a.id
LIMIT 1
FOR UPDATE OF a SKIP LOCKED;

-- name: InsertRun :one
INSERT INTO runs (application_id, user_id, runner_id, host, token_hash, leased_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id;

-- name: RunByToken :one
-- System role: the run a token belongs to.
SELECT id, application_id, user_id, expires_at, finished_at FROM runs WHERE token_hash = $1;

-- name: FinishRun :exec
UPDATE runs SET finished_at = $3, outcome = $4 WHERE id = $1 AND user_id = $2 AND finished_at IS NULL;

-- name: InsertCheckpoint :exec
INSERT INTO run_checkpoints (run_id, user_id, step, page, url, data, screenshot, at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: RunJob :one
SELECT j.id, j.url, j.apply_url, j.company, j.title, j.location, j.description FROM runs r
JOIN applications a ON a.id = r.application_id JOIN jobs j ON j.id = a.job_id
WHERE r.id = $1 AND r.user_id = $2;

-- name: LockLeases :exec
-- Leases run one at a time, so that two runners never pass the per-host check together.
SELECT pg_advisory_xact_lock(7303470200);
