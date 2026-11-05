-- Recovery (ADR 0022): once a user has a passkey, a mail link signs in only after a waiting period.

-- +goose Up
CREATE TABLE recoveries (
  user_id      uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  requested_at timestamptz NOT NULL,
  ready_at     timestamptz NOT NULL,
  expires_at   timestamptz NOT NULL,
  CHECK (requested_at < ready_at AND ready_at < expires_at)
);
ALTER TABLE recoveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE recoveries FORCE ROW LEVEL SECURITY;
CREATE POLICY recoveries_tenant ON recoveries
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE recoveries;
