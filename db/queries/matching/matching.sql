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

