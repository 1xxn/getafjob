-- Runs (#30): a runner leases one application at a time; the run's token is stored as a hash only. Per-host
-- limits are applied at leasing, checkpoints keep each step (screenshots live sealed in the blob store).

-- +goose Up
CREATE TABLE runs (
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  application_id uuid NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
  user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  runner_id      text NOT NULL CHECK (length(runner_id) <= 100),
  host           text NOT NULL CHECK (length(host) <= 253),
  token_hash     bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  leased_at      timestamptz NOT NULL,
  expires_at     timestamptz NOT NULL,
  finished_at    timestamptz,
  outcome        text CHECK (outcome IN ('asked', 'submitted', 'parked', 'guard', 'expired'))
);
CREATE INDEX runs_host ON runs (host, leased_at DESC);
CREATE INDEX runs_application ON runs (application_id) WHERE finished_at IS NULL;

CREATE TABLE run_checkpoints (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  run_id     uuid NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
  user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  step       text NOT NULL CHECK (length(step) <= 20),
  page       integer NOT NULL CHECK (page BETWEEN 0 AND 100),
  url        text NOT NULL CHECK (length(url) <= 2000),
  data       jsonb NOT NULL DEFAULT '{}' CHECK (length(data::text) <= 262144),
  screenshot text NOT NULL DEFAULT '' CHECK (length(screenshot) <= 200),
  at         timestamptz NOT NULL
);
CREATE INDEX run_checkpoints_run ON run_checkpoints (run_id, id);

ALTER TABLE runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE runs FORCE ROW LEVEL SECURITY;
CREATE POLICY runs_tenant ON runs
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());
ALTER TABLE run_checkpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE run_checkpoints FORCE ROW LEVEL SECURITY;
CREATE POLICY run_checkpoints_tenant ON run_checkpoints
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE run_checkpoints;
DROP TABLE runs;
