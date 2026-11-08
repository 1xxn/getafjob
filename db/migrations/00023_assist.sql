-- Assist (#39): pairing codes and tokens for the browser extension.

-- +goose Up
CREATE TABLE assist_pairings (
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  code_hash   bytea NOT NULL UNIQUE,
  created_at  timestamptz NOT NULL,
  expires_at  timestamptz NOT NULL
);
CREATE INDEX assist_pairings_user ON assist_pairings (user_id);

CREATE TABLE assist_tokens (
  id           uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  token_hash   bytea NOT NULL UNIQUE,
  client_name  text NOT NULL CHECK (length(client_name) <= 128),
  created_at   timestamptz NOT NULL,
  expires_at   timestamptz NOT NULL,
  last_used_at timestamptz
);
CREATE INDEX assist_tokens_user ON assist_tokens (user_id);

ALTER TABLE assist_pairings ENABLE ROW LEVEL SECURITY;
ALTER TABLE assist_pairings FORCE ROW LEVEL SECURITY;
CREATE POLICY assist_pairings_tenant ON assist_pairings
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

ALTER TABLE assist_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE assist_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY assist_tokens_tenant ON assist_tokens
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE assist_tokens;
DROP TABLE assist_pairings;
