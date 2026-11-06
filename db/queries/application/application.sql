-- name: CreateApplication :one
INSERT INTO applications (user_id, job_id, state, created_at, updated_at) VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (user_id, job_id) DO UPDATE SET user_id = EXCLUDED.user_id
RETURNING id;

-- name: GetApplication :one
SELECT id, job_id, state, version, updated_at FROM applications WHERE id = $1 AND user_id = $2;

-- name: SetState :execrows
UPDATE applications SET state = sqlc.arg(to_state), version = version + 1, updated_at = sqlc.arg(at)
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
