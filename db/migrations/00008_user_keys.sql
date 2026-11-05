-- Envelope encryption (ADR 0006): one data key per user, wrapped with the master key.

-- +goose Up
CREATE TABLE user_keys (
  user_id       uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  master_key_id text NOT NULL CHECK (length(master_key_id) BETWEEN 1 AND 64),
  wrapped       bytea NOT NULL CHECK (length(wrapped) BETWEEN 32 AND 256),
  created_at    timestamptz NOT NULL,
  rotated_at    timestamptz
);
ALTER TABLE user_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY user_keys_tenant ON user_keys
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE user_keys;
