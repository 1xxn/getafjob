-- Push subscriptions (#31): Web Push endpoints with P-256 keys and auth secret for handoff notices.
-- Row-level security: users only see and manage their own subscriptions; system role delivers notices.

-- +goose Up
CREATE TABLE push_subscriptions (
  id         uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  endpoint   text NOT NULL UNIQUE CHECK (length(endpoint) <= 2000),
  p256dh     text NOT NULL CHECK (length(p256dh) <= 200),
  auth       text NOT NULL CHECK (length(auth) <= 100),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX push_subscriptions_user ON push_subscriptions (user_id);

ALTER TABLE push_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE push_subscriptions FORCE ROW LEVEL SECURITY;
CREATE POLICY push_subscriptions_tenant ON push_subscriptions
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE push_subscriptions;
