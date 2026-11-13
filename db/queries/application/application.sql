-- name: CreateApplication :one
INSERT INTO applications (user_id, job_id, state, created_at, updated_at) VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (user_id, job_id) DO UPDATE SET user_id = EXCLUDED.user_id
RETURNING id;

-- name: GetApplication :one
SELECT id, job_id, state, version, updated_at, approval_asked_at, approval_reasons, approved_at, cv_document_id
FROM applications WHERE id = $1 AND user_id = $2;

-- name: GetApplicationByJob :one
SELECT id, job_id, state, version, updated_at, approval_asked_at, approval_reasons, approved_at, cv_document_id
FROM applications WHERE job_id = $1 AND user_id = $2;

-- name: SetCvDocument :execrows
UPDATE applications SET cv_document_id = sqlc.narg(cv_document_id)::uuid, updated_at = sqlc.arg(now)::timestamptz
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: SetJobCvDocument :one
INSERT INTO applications (user_id, job_id, state, cv_document_id, created_at, updated_at)
VALUES ($1, $2, 'matched', sqlc.narg(cv_document_id)::uuid, sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz)
ON CONFLICT (user_id, job_id) DO UPDATE SET cv_document_id = EXCLUDED.cv_document_id, updated_at = EXCLUDED.updated_at
RETURNING id;

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
-- System role: the first queued application (queue order, not paused) whose host has no active run and no lease within the gap,
-- whose user has free daily quota (both skipped when unlimited), that has no unfinished run itself, and that
-- matches the runner's choice.
SELECT a.id, a.user_id, a.job_id,
  lower(coalesce(substring(coalesce(nullif(j.apply_url, ''), j.url) from '^[a-zA-Z]+://([^/:?#]+)'), ''))::text AS host
FROM applications a
JOIN jobs j ON j.id = a.job_id
LEFT JOIN autonomy_settings s ON s.user_id = a.user_id
WHERE a.state = 'queued'
  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.application_id = a.id AND r.finished_at IS NULL AND r.expires_at > sqlc.arg(now)::timestamptz)
  -- Without limits (self-host) neither the per-host gap nor the daily quota applies.
  AND (sqlc.arg(unlimited)::bool OR (
    NOT EXISTS (SELECT 1 FROM runs r WHERE r.host = lower(coalesce(substring(coalesce(nullif(j.apply_url, ''), j.url) from '^[a-zA-Z]+://([^/:?#]+)'), ''))
      AND ((r.finished_at IS NULL AND r.expires_at > sqlc.arg(now)::timestamptz) OR r.leased_at > sqlc.arg(gap_start)::timestamptz))
    AND coalesce(s.daily_limit, 5) > 0
    AND (
      SELECT count(*) FROM submit_attempts sa
      WHERE sa.user_id = a.user_id
        AND sa.started_at >= sqlc.arg(now)::timestamptz - interval '24 hours'
    ) < coalesce(s.daily_limit, 5)))
  AND coalesce(s.runner_choice, 'server') = sqlc.arg(runner_choice)::text
  AND (sqlc.narg(user_id)::uuid IS NULL OR a.user_id = sqlc.narg(user_id)::uuid)
  AND NOT a.queue_paused
ORDER BY a.queue_pos NULLS LAST, a.id
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

-- name: FinishAppRuns :exec
-- Ends the open runs of an application that left the run states, so their tokens stop working.
UPDATE runs SET finished_at = $3, outcome = $4 WHERE application_id = $1 AND user_id = $2 AND finished_at IS NULL;

-- name: InsertCheckpoint :exec
INSERT INTO run_checkpoints (run_id, user_id, step, page, url, data, screenshot, at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: RunJob :one
SELECT j.id, j.url, j.apply_url, j.company, j.title, j.location, j.description FROM runs r
JOIN applications a ON a.id = r.application_id JOIN jobs j ON j.id = a.job_id
WHERE r.id = $1 AND r.user_id = $2;

-- name: LockLeases :exec
-- Leases run one at a time, so that two runners never pass the per-host check together.
SELECT pg_advisory_xact_lock(7303470200);

-- name: GetJob :one
SELECT id, source, company, title, location, url, apply_url, description FROM jobs WHERE id = $1;

-- name: ApplicationCheckpoints :many
SELECT c.id, c.run_id, c.step, c.page, c.url, c.data, c.screenshot, c.at
FROM run_checkpoints c JOIN runs r ON r.id = c.run_id
WHERE r.application_id = $1 AND c.user_id = $2
ORDER BY c.id;

-- name: CheckpointScreenshot :one
SELECT c.screenshot
FROM run_checkpoints c JOIN runs r ON r.id = c.run_id
WHERE r.application_id = $1 AND c.id = $2 AND c.user_id = $3;

-- name: ExpiredCheckpointScreenshots :many
-- System role: checkpoint screenshots older than the retention window.
SELECT id, user_id, screenshot FROM run_checkpoints
WHERE screenshot <> '' AND at < $1
ORDER BY at, id
LIMIT 500;

-- name: ClearCheckpointScreenshot :exec
UPDATE run_checkpoints SET screenshot = '' WHERE id = $1 AND user_id = $2;

-- name: UserApplications :many
SELECT a.id, a.job_id, a.state, a.version, a.created_at, a.updated_at,
       a.approval_asked_at, a.approval_reasons, a.approved_at,
       j.company, j.title, j.location, j.url, j.apply_url
FROM applications a
JOIN jobs j ON j.id = a.job_id
WHERE a.user_id = $1
ORDER BY a.created_at DESC;

-- name: UserRunScreenshots :many
SELECT DISTINCT screenshot FROM run_checkpoints
WHERE user_id = $1 AND screenshot <> '';

-- name: GetNotes :one
SELECT id, user_id, application_id, notes, salary_discussed, interview_at, follow_up_at, created_at, updated_at
FROM application_notes
WHERE application_id = $1 AND user_id = $2;

-- name: UpsertNotes :one
INSERT INTO application_notes (id, user_id, application_id, notes, salary_discussed, interview_at, follow_up_at, created_at, updated_at)
VALUES (uuidv7(), $1, $2, $3, $4, sqlc.narg(interview_at)::timestamptz, sqlc.narg(follow_up_at)::timestamptz, sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz)
ON CONFLICT (application_id) DO UPDATE SET
  notes = EXCLUDED.notes,
  salary_discussed = EXCLUDED.salary_discussed,
  interview_at = EXCLUDED.interview_at,
  follow_up_at = EXCLUDED.follow_up_at,
  updated_at = EXCLUDED.updated_at
RETURNING id;

-- name: UserApplicationsWithPipeline :many
SELECT a.id, a.job_id, a.state, a.version, a.created_at, a.updated_at,
       a.approval_asked_at, a.approval_reasons, a.approved_at, a.cv_document_id, a.handoff_until,
       j.company, j.title, j.location, j.workplace, j.url, j.apply_url,
       COALESCE(m.score, 0)::integer AS score,
       COALESCE(n.notes, '')::text AS notes,
       COALESCE(n.salary_discussed, '')::text AS salary_discussed,
       n.interview_at, n.follow_up_at
FROM applications a
JOIN jobs j ON j.id = a.job_id
LEFT JOIN matches m ON m.job_id = a.job_id AND m.user_id = a.user_id
LEFT JOIN application_notes n ON n.application_id = a.id AND n.user_id = a.user_id
WHERE a.user_id = $1
ORDER BY a.updated_at DESC;

-- name: SaveBookmark :one
INSERT INTO applications (user_id, job_id, state, created_at, updated_at)
VALUES ($1, $2, 'matched', sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz)
ON CONFLICT (user_id, job_id) DO UPDATE SET updated_at = EXCLUDED.updated_at
RETURNING id, state;

-- name: UnfinishedRuns :many
-- System role: runs without a finish whose application still waits on a runner (running or needs_input).
SELECT r.id, r.application_id, r.user_id, r.leased_at
FROM runs r
JOIN applications a ON a.id = r.application_id
WHERE r.finished_at IS NULL AND a.state IN ('running', 'needs_input')
ORDER BY r.leased_at
LIMIT 500;

-- name: ParkReasons :many
SELECT DISTINCT ON (e.application_id) e.application_id, coalesce(e.detail->>'reason', e.detail->>'guard', '')::text AS reason
FROM application_events e
JOIN applications a ON a.id = e.application_id
WHERE a.user_id = $1 AND a.state = 'parked' AND e.to_state = 'parked'
ORDER BY e.application_id, e.id DESC;

-- name: SubmitsPerDay :many
SELECT (e.at AT TIME ZONE 'UTC')::date AS day, count(*)::integer AS n
FROM application_events e
WHERE e.user_id = $1 AND e.to_state = 'submitted' AND e.at >= $2
GROUP BY 1
ORDER BY 1;

-- name: StatesByJob :many
SELECT job_id, state FROM applications WHERE user_id = $1 AND job_id = ANY (sqlc.arg(job_ids)::bigint[]);

-- name: AnswerUsage :many
-- How many applications used each answer of the bank (provenance "answer#<id>" in the filled fields).
SELECT substring(f->>'source' from 8)::text AS answer_id, count(DISTINCT r.application_id)::integer AS n
FROM run_checkpoints c
JOIN runs r ON r.id = c.run_id
CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(c.data->'filled') = 'array' THEN c.data->'filled' ELSE '[]'::jsonb END) f
WHERE c.user_id = $1 AND f->>'source' LIKE 'answer#%'
GROUP BY 1;

-- name: ApplicationBriefs :many
SELECT a.id, a.job_id, a.state, j.title, j.company
FROM applications a JOIN jobs j ON j.id = a.job_id
WHERE a.user_id = $1 AND a.id = ANY (sqlc.arg(ids)::uuid[]);

-- name: QueuedApplications :many
-- The user's queue in the order the runs start.
SELECT a.id, a.job_id, j.title, j.company, a.queue_paused
FROM applications a JOIN jobs j ON j.id = a.job_id
WHERE a.user_id = $1 AND a.state = 'queued'
ORDER BY a.queue_pos NULLS LAST, a.id;

-- name: SetQueuePaused :execrows
UPDATE applications SET queue_paused = $3 WHERE user_id = $1 AND id = $2 AND state = 'queued';

-- name: MoveQueued :execrows
-- Swaps the queue position of a queued application with its neighbor before (up) or after it.
WITH me AS (
  SELECT m.id, m.queue_pos FROM applications m
  WHERE m.user_id = $1 AND m.id = $2 AND m.state = 'queued' AND m.queue_pos IS NOT NULL
  FOR UPDATE OF m
), nb AS (
  SELECT a.id, a.queue_pos FROM applications a, me
  WHERE a.user_id = $1 AND a.state = 'queued' AND a.queue_pos IS NOT NULL AND a.id <> me.id
    AND CASE WHEN sqlc.arg(up)::bool THEN a.queue_pos <= me.queue_pos ELSE a.queue_pos >= me.queue_pos END
  ORDER BY CASE WHEN sqlc.arg(up)::bool THEN -a.queue_pos ELSE a.queue_pos END, a.id
  LIMIT 1
  FOR UPDATE OF a
)
UPDATE applications a SET queue_pos = CASE WHEN a.id = me.id THEN nb.queue_pos ELSE me.queue_pos END
FROM me, nb
WHERE a.user_id = $1 AND a.id IN (me.id, nb.id);
