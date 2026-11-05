-- name: StartRun :one
INSERT INTO crawl_runs (started_at, sources) VALUES ($1, $2) RETURNING id;

-- name: FinishRun :exec
UPDATE crawl_runs SET finished_at = $2, stats = $3, errors = $4 WHERE id = $1;

-- name: JobByFingerprint :one
SELECT id, title, location, description, content_hash, status, url, alt_urls FROM jobs WHERE fingerprint = $1 FOR UPDATE;

-- name: OpenJobsByMatchKey :many
SELECT id, source, location, url, alt_urls FROM jobs WHERE match_key = $1 AND status = 'open' ORDER BY id;

-- name: InsertJob :one
INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, location, workplace, url, apply_url,
                  description, posted, content_hash, first_seen, last_seen)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
RETURNING id;

-- name: UpdateJob :exec
-- Empty fields keep the stored value, except apply_url on a merge (the job-board link would be wrong).
UPDATE jobs
SET fingerprint  = sqlc.arg(fingerprint),
    match_key    = sqlc.arg(match_key),
    source       = sqlc.arg(source),
    external_id  = sqlc.arg(external_id),
    company      = coalesce(nullif(sqlc.arg(company)::text, ''), company),
    title        = sqlc.arg(title),
    location     = sqlc.arg(location),
    workplace    = coalesce(nullif(sqlc.arg(workplace)::text, ''), workplace),
    url          = sqlc.arg(url),
    apply_url    = CASE WHEN sqlc.arg(merge)::bool THEN sqlc.arg(apply_url)::text
                        ELSE coalesce(nullif(sqlc.arg(apply_url)::text, ''), apply_url) END,
    alt_urls     = sqlc.arg(alt_urls)::text[],
    description  = coalesce(nullif(sqlc.arg(description)::text, ''), description),
    posted       = coalesce(sqlc.narg(posted)::date, posted),
    content_hash = sqlc.arg(content_hash),
    last_seen    = sqlc.arg(last_seen),
    status       = 'open',
    closed_at    = NULL
WHERE id = sqlc.arg(id);

-- name: LockMatchKey :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(match_key)::text, 0));

-- name: InsertSnapshot :exec
INSERT INTO job_snapshots (job_id, crawl_run, change, title, location, content_hash, diff, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: OpenJobsOfSources :many
SELECT id, fingerprint, source, last_seen FROM jobs WHERE status = 'open' AND source = ANY(sqlc.arg(sources)::text[]);

-- name: CloseJob :one
UPDATE jobs SET status = 'closed', closed_at = $2 WHERE id = $1 AND status = 'open'
RETURNING title, location, content_hash;

-- name: DescribedJobs :many
SELECT fingerprint FROM jobs WHERE fingerprint = ANY(sqlc.arg(fingerprints)::text[]) AND description <> '';

-- name: UpsertBoard :one
-- Keeps enabled, origin and crawl state; refreshes name and config.
INSERT INTO boards (ats, slug, company, config, origin) VALUES ($1, $2, $3, $4, 'admin')
ON CONFLICT (ats, slug) DO UPDATE SET company = EXCLUDED.company, config = EXCLUDED.config
RETURNING (xmax = 0)::bool AS inserted;

-- name: EnabledBoards :many
SELECT id, ats, slug, company, config FROM boards WHERE enabled ORDER BY id;
