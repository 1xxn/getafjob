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
-- One page of the user's open jobs, best first; total counts all hits of the filter.
SELECT j.id, j.title, j.company, j.location, j.workplace, j.posted, j.first_seen, m.score, m.decision,
  count(*) OVER () AS total
FROM matches m JOIN jobs j ON j.id = m.job_id
WHERE m.user_id = sqlc.arg(user_id) AND j.status = 'open'
  AND (cardinality(sqlc.arg(decisions)::text[]) = 0 OR m.decision = ANY (sqlc.arg(decisions)::text[]))
  AND (sqlc.arg(workplace)::text = '' OR j.workplace = sqlc.arg(workplace)::text)
  AND (sqlc.arg(query)::text = '' OR j.search @@ (websearch_to_tsquery('simple', sqlc.arg(query)::text)
    || websearch_to_tsquery('english', sqlc.arg(query)::text) || websearch_to_tsquery('german', sqlc.arg(query)::text)))
ORDER BY m.score DESC, j.id DESC
LIMIT sqlc.arg(page_size)::integer OFFSET sqlc.arg(page_offset)::integer;

-- name: MatchCounts :many
SELECT m.decision, count(*)::bigint AS n FROM matches m JOIN jobs j ON j.id = m.job_id
WHERE m.user_id = $1 AND j.status = 'open' GROUP BY m.decision;

-- name: GetMatch :one
SELECT j.id, j.title, j.company, j.location, j.workplace, j.posted, j.first_seen, j.description, j.url,
  j.apply_url, j.status, m.score, m.decision, m.reasons, m.computed_at
FROM matches m JOIN jobs j ON j.id = m.job_id WHERE m.user_id = $1 AND j.id = $2;

-- name: UserMatches :many
SELECT j.id, j.title, j.company, j.location, j.workplace, m.score, m.decision, m.reasons, m.computed_at
FROM matches m JOIN jobs j ON j.id = m.job_id
WHERE m.user_id = $1
ORDER BY m.score DESC, j.id DESC;
