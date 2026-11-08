-- Autonomy (#35, ADR 0016): the user's level, score threshold and daily limit; the approval list on applications;
-- the ATS adapter per run, whose finished runs measure its maturity.

-- +goose Up
CREATE TABLE autonomy_settings (
  user_id     uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  level       text NOT NULL DEFAULT 'approve' CHECK (level IN ('suggest', 'approve', 'autonomous')),
  min_score   smallint NOT NULL DEFAULT 80 CHECK (min_score BETWEEN 0 AND 100),
  daily_limit smallint NOT NULL DEFAULT 5 CHECK (daily_limit BETWEEN 0 AND 50),
  updated_at  timestamptz NOT NULL
);
ALTER TABLE autonomy_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE autonomy_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY autonomy_settings_tenant ON autonomy_settings
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

ALTER TABLE applications
  ADD COLUMN approval_asked_at timestamptz,
  ADD COLUMN approval_reasons  text[] NOT NULL DEFAULT '{}' CHECK (cardinality(approval_reasons) <= 20),
  ADD COLUMN approved_at       timestamptz;
CREATE INDEX applications_approvals ON applications (user_id, approval_asked_at)
  WHERE approval_asked_at IS NOT NULL AND approved_at IS NULL;

ALTER TABLE runs ADD COLUMN adapter text NOT NULL DEFAULT '' CHECK (length(adapter) <= 40);
CREATE INDEX runs_adapter ON runs (adapter, finished_at) WHERE finished_at IS NOT NULL;

-- +goose Down
DROP INDEX runs_adapter;
ALTER TABLE runs DROP COLUMN adapter;
DROP INDEX applications_approvals;
ALTER TABLE applications DROP COLUMN approved_at, DROP COLUMN approval_reasons, DROP COLUMN approval_asked_at;
DROP TABLE autonomy_settings;
