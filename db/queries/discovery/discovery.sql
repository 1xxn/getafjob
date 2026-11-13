-- name: StartRun :one
INSERT INTO crawl_runs (started_at, sources) VALUES ($1, $2) RETURNING id;

-- name: FinishRun :exec
UPDATE crawl_runs SET finished_at = $2, stats = $3, errors = $4 WHERE id = $1;

-- name: LatestCrawlRun :one
SELECT id, started_at, finished_at, sources, stats, errors
FROM crawl_runs
WHERE finished_at IS NOT NULL
ORDER BY id DESC
LIMIT 1;

-- name: ActiveCrawlRun :one
SELECT id, started_at, sources
FROM crawl_runs
WHERE finished_at IS NULL AND started_at > sqlc.arg(since)::timestamptz
ORDER BY id DESC
LIMIT 1;

-- name: JobByFingerprint :one
SELECT id, title, location, description, content_hash, status, url, alt_urls FROM jobs WHERE fingerprint = $1 FOR UPDATE;

-- name: OpenJobsByMatchKey :many
SELECT id, source, location, url, alt_urls FROM jobs WHERE match_key = $1 AND status = 'open' ORDER BY id;

-- name: InsertJob :one
INSERT INTO jobs (fingerprint, match_key, source, external_id, company, title, location, workplace, url, apply_url,
                  description, posted, salary_min, salary_max, salary_currency, content_hash, first_seen, last_seen)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
        sqlc.narg(salary_min)::integer, sqlc.narg(salary_max)::integer,
        coalesce(nullif(sqlc.narg(salary_currency)::text, ''), 'EUR'),
        $13, $14, $14)
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
    salary_min   = coalesce(sqlc.narg(salary_min)::integer, salary_min),
    salary_max   = coalesce(sqlc.narg(salary_max)::integer, salary_max),
    salary_currency = coalesce(nullif(sqlc.arg(salary_currency)::text, ''), salary_currency, 'EUR'),
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

-- name: StaleImportedJobs :many
SELECT id, fingerprint, url FROM jobs
WHERE status = 'open' AND source LIKE 'import:%' AND last_seen < sqlc.arg(seen_before)
ORDER BY last_seen, id
LIMIT sqlc.arg(max_rows);

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
SELECT id, ats, slug, company, config, coalesce(etag, '')::text AS etag, coalesce(last_modified, '')::text AS last_modified
FROM boards WHERE enabled ORDER BY id;

-- name: TouchOpenJobs :many
UPDATE jobs SET last_seen = sqlc.arg(at) WHERE fingerprint = ANY(sqlc.arg(fingerprints)::text[]) AND status = 'open'
RETURNING fingerprint;

-- name: BoardCandidates :many
-- Open job-board hits of companies whose own board was not looked for recently, newest first.
SELECT company, title, url, apply_url, match_key
FROM jobs j
WHERE j.status = 'open'
  AND split_part(j.source, ':', 1) = ANY(sqlc.arg(aggregators)::text[])
  AND split_part(j.match_key, '|', 1) <> ''
  AND NOT EXISTS (SELECT 1 FROM board_checks c
                  WHERE c.company_key = split_part(j.match_key, '|', 1) AND c.recheck_at > sqlc.arg(now))
ORDER BY j.first_seen DESC, j.id
LIMIT sqlc.arg(max_rows);

-- name: BoardBySlug :one
SELECT id FROM boards WHERE ats = sqlc.arg(ats) AND lower(slug) = lower(sqlc.arg(slug)::text) ORDER BY id LIMIT 1;

-- name: InsertDiscoveredBoard :one
INSERT INTO boards (ats, slug, company, config, origin) VALUES ($1, $2, $3, $4, 'discovered')
ON CONFLICT (ats, slug) DO NOTHING
RETURNING id;

-- name: RecordBoardCheck :exec
INSERT INTO board_checks (company_key, checked_at, recheck_at, result, board_id) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (company_key) DO UPDATE
SET checked_at = EXCLUDED.checked_at, recheck_at = EXCLUDED.recheck_at, result = EXCLUDED.result,
    board_id = EXCLUDED.board_id;

-- name: SaveBoardState :exec
-- A failed fetch keeps the validators of the last good one.
UPDATE boards
SET last_crawled_at = sqlc.arg(at),
    last_error      = nullif(sqlc.arg(last_error)::text, ''),
    etag            = CASE WHEN sqlc.arg(last_error)::text = '' THEN nullif(sqlc.arg(etag)::text, '') ELSE etag END,
    last_modified   = CASE WHEN sqlc.arg(last_error)::text = '' THEN nullif(sqlc.arg(last_modified)::text, '') ELSE last_modified END
WHERE id = sqlc.arg(id);
