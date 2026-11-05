-- Matches (#20): score, decision and reasons per user and job; recomputed after crawls and profile changes.

-- +goose Up
CREATE TABLE matches (
  user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  job_id      bigint NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
  score       smallint NOT NULL CHECK (score BETWEEN 0 AND 100),
  decision    text NOT NULL CHECK (decision IN ('match', 'maybe', 'skip')),
  reasons     jsonb NOT NULL DEFAULT '[]' CHECK (length(reasons::text) <= 16384),
  rules       integer NOT NULL,
  computed_at timestamptz NOT NULL,
  PRIMARY KEY (user_id, job_id)
);
CREATE INDEX matches_rank ON matches (user_id, decision, score DESC);
CREATE INDEX matches_job ON matches (job_id);
ALTER TABLE matches ENABLE ROW LEVEL SECURITY;
ALTER TABLE matches FORCE ROW LEVEL SECURITY;
CREATE POLICY matches_tenant ON matches
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE matches;
