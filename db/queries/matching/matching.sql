-- name: OpenJobsPage :many
SELECT id, company, title, location, workplace, description, posted, first_seen FROM jobs
WHERE status = 'open' AND id > $1 ORDER BY id LIMIT $2;

-- name: SaveMatches :exec
-- Rows that did not change keep their computed_at, so unchanged results cost no write.
INSERT INTO matches (user_id, job_id, score, decision, reasons, rules, computed_at)
SELECT sqlc.arg(user_id)::uuid, r.job_id, r.score, r.decision, r.reasons, sqlc.arg(rules)::integer, sqlc.arg(computed_at)::timestamptz
FROM jsonb_to_recordset(sqlc.arg(results)::jsonb) AS r (job_id bigint, score smallint, decision text, reasons jsonb)
ON CONFLICT (user_id, job_id) DO UPDATE SET score = EXCLUDED.score, decision = EXCLUDED.decision,
  reasons = EXCLUDED.reasons, rules = EXCLUDED.rules, computed_at = EXCLUDED.computed_at
WHERE (matches.score, matches.decision, matches.reasons, matches.rules)
  IS DISTINCT FROM (EXCLUDED.score, EXCLUDED.decision, EXCLUDED.reasons, EXCLUDED.rules);

-- name: ListMatches :many
-- One page of the user's open jobs, ordered by criteria; total counts all hits of the filter.
SELECT j.id, j.title, j.company, j.location, j.workplace, j.posted, j.first_seen,
  coalesce(j.salary_min, 0)::integer AS salary_min,
  coalesce(j.salary_max, 0)::integer AS salary_max,
  j.salary_currency, j.source, m.score, m.decision,
  EXISTS (
    SELECT 1 FROM user_blocklist b
    WHERE b.user_id = m.user_id
      AND (
        (b.kind = 'company' AND j.company ILIKE '%' || b.pattern || '%')
        OR (b.kind = 'keyword' AND (j.company ILIKE '%' || b.pattern || '%' OR j.title ILIKE '%' || b.pattern || '%' OR j.description ILIKE '%' || b.pattern || '%'))
      )
  ) AS blocked,
  count(*) OVER () AS total
FROM matches m JOIN jobs j ON j.id = m.job_id
WHERE m.user_id = sqlc.arg(user_id) AND j.status = 'open'
  AND (cardinality(sqlc.arg(decisions)::text[]) = 0 OR m.decision = ANY (sqlc.arg(decisions)::text[]))
  AND (sqlc.arg(workplace)::text = '' OR j.workplace = sqlc.arg(workplace)::text)
  AND (sqlc.arg(location)::text = '' OR j.location ILIKE '%' || sqlc.arg(location)::text || '%')
  AND (sqlc.arg(source)::text = '' OR j.source = sqlc.arg(source)::text)
  AND (sqlc.arg(min_salary)::integer = 0 OR coalesce(j.salary_max, j.salary_min, 0) >= sqlc.arg(min_salary)::integer)
  AND (sqlc.arg(freshness_days)::integer = 0 OR coalesce(j.posted::timestamptz, j.first_seen) >= (now() - (sqlc.arg(freshness_days)::text || ' days')::interval))
  AND (sqlc.narg(seen_after)::timestamptz IS NULL OR j.first_seen > sqlc.narg(seen_after)::timestamptz)
  AND (sqlc.narg(seen_before)::timestamptz IS NULL OR j.first_seen <= sqlc.narg(seen_before)::timestamptz)
  AND (sqlc.arg(query)::text = '' OR j.search @@ (websearch_to_tsquery('simple', sqlc.arg(query)::text)
    || websearch_to_tsquery('english', sqlc.arg(query)::text) || websearch_to_tsquery('german', sqlc.arg(query)::text)))
  AND (
    CASE WHEN sqlc.arg(show_blocked)::boolean THEN
      EXISTS (
        SELECT 1 FROM user_blocklist b
        WHERE b.user_id = m.user_id
          AND (
            (b.kind = 'company' AND j.company ILIKE '%' || b.pattern || '%')
            OR (b.kind = 'keyword' AND (j.company ILIKE '%' || b.pattern || '%' OR j.title ILIKE '%' || b.pattern || '%' OR j.description ILIKE '%' || b.pattern || '%'))
          )
      )
    ELSE
      NOT EXISTS (
        SELECT 1 FROM user_blocklist b
        WHERE b.user_id = m.user_id
          AND (
            (b.kind = 'company' AND j.company ILIKE '%' || b.pattern || '%')
            OR (b.kind = 'keyword' AND (j.company ILIKE '%' || b.pattern || '%' OR j.title ILIKE '%' || b.pattern || '%' OR j.description ILIKE '%' || b.pattern || '%'))
          )
      )
    END
  )
ORDER BY
  CASE WHEN sqlc.arg(sort)::text = 'newest' THEN coalesce(j.posted::timestamptz, j.first_seen) END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort)::text = 'salary' THEN coalesce(j.salary_max, j.salary_min, 0) END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort)::text = 'company' THEN lower(j.company) END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'match' OR sqlc.arg(sort)::text = '' THEN m.score END DESC,
  j.id DESC
LIMIT sqlc.arg(page_size)::integer OFFSET sqlc.arg(page_offset)::integer;

-- name: MatchCounts :many
SELECT m.decision, count(*)::bigint AS n FROM matches m JOIN jobs j ON j.id = m.job_id
WHERE m.user_id = $1 AND j.status = 'open'
  AND NOT EXISTS (
    SELECT 1 FROM user_blocklist b
    WHERE b.user_id = m.user_id
      AND (
        (b.kind = 'company' AND j.company ILIKE '%' || b.pattern || '%')
        OR (b.kind = 'keyword' AND (j.company ILIKE '%' || b.pattern || '%' OR j.title ILIKE '%' || b.pattern || '%' OR j.description ILIKE '%' || b.pattern || '%'))
      )
  )
GROUP BY m.decision;

-- name: BlockedCount :one
SELECT count(*)::bigint AS n FROM matches m JOIN jobs j ON j.id = m.job_id
WHERE m.user_id = $1 AND j.status = 'open'
  AND EXISTS (
    SELECT 1 FROM user_blocklist b
    WHERE b.user_id = m.user_id
      AND (
        (b.kind = 'company' AND j.company ILIKE '%' || b.pattern || '%')
        OR (b.kind = 'keyword' AND (j.company ILIKE '%' || b.pattern || '%' OR j.title ILIKE '%' || b.pattern || '%' OR j.description ILIKE '%' || b.pattern || '%'))
      )
  );

-- name: GetMatch :one
SELECT j.id, j.title, j.company, j.location, j.workplace, j.posted, j.first_seen, j.description, j.url,
  j.apply_url, j.status,
  coalesce(j.salary_min, 0)::integer AS salary_min,
  coalesce(j.salary_max, 0)::integer AS salary_max,
  j.salary_currency, j.source, m.score, m.decision, m.reasons, m.computed_at,
  EXISTS (
    SELECT 1 FROM user_blocklist b
    WHERE b.user_id = m.user_id
      AND (
        (b.kind = 'company' AND j.company ILIKE '%' || b.pattern || '%')
        OR (b.kind = 'keyword' AND (j.company ILIKE '%' || b.pattern || '%' OR j.title ILIKE '%' || b.pattern || '%' OR j.description ILIKE '%' || b.pattern || '%'))
      )
  ) AS blocked
FROM matches m JOIN jobs j ON j.id = m.job_id WHERE m.user_id = $1 AND j.id = $2;

-- name: UserMatches :many
SELECT j.id, j.title, j.company, j.location, j.workplace, m.score, m.decision, m.reasons, m.computed_at
FROM matches m JOIN jobs j ON j.id = m.job_id
WHERE m.user_id = $1
ORDER BY m.score DESC, j.id DESC;

-- name: ListBlocklist :many
SELECT id, user_id, kind, pattern, created_at
FROM user_blocklist
WHERE user_id = $1
ORDER BY created_at DESC, id DESC;

-- name: InsertBlocklist :exec
INSERT INTO user_blocklist (user_id, kind, pattern)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, kind, pattern) DO NOTHING;

-- name: DeleteBlocklist :exec
DELETE FROM user_blocklist
WHERE user_id = $1 AND id = $2;

-- name: DeleteBlocklistByPattern :exec
DELETE FROM user_blocklist
WHERE user_id = $1 AND kind = $2 AND lower(pattern) = lower($3);
