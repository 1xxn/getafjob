-- Identity (ADR 0020): invitations, magic links, sessions, passkeys. Tokens are stored only as SHA-256 hashes.

-- +goose Up
CREATE TABLE invitations (
  id            uuid PRIMARY KEY DEFAULT uuidv7(),
  email         citext NOT NULL CHECK (length(email) <= 254),
  role          text NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
  token_hash    bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  created_by    uuid REFERENCES users (id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  accepted_at   timestamptz,
  accepted_user uuid REFERENCES users (id) ON DELETE SET NULL
);
-- Invitations exist before their user does: system context only.
ALTER TABLE invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY invitations_system ON invitations USING (app_is_system()) WITH CHECK (app_is_system());

CREATE TABLE magic_links (
  token_hash bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  used_at    timestamptz
);
CREATE INDEX magic_links_user ON magic_links (user_id);

CREATE TABLE sessions (
  token_hash   bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  method       text NOT NULL CHECK (method IN ('invitation', 'magic_link', 'passkey')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE passkeys (
  id           bytea PRIMARY KEY CHECK (length(id) BETWEEN 16 AND 1023),
  user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name         text NOT NULL DEFAULT '' CHECK (length(name) <= 100),
  credential   jsonb NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz
);
CREATE INDEX passkeys_user ON passkeys (user_id);

CREATE TABLE webauthn_challenges (
  id         uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id    uuid REFERENCES users (id) ON DELETE CASCADE,
  kind       text NOT NULL CHECK (kind IN ('register', 'login')),
  data       jsonb NOT NULL,
  expires_at timestamptz NOT NULL
);

-- Sign-in happens before the user is known (system context); afterwards users see their own rows.
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['magic_links', 'sessions', 'passkeys', 'webauthn_challenges'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY %I ON %I USING (user_id = app_user_id() OR app_is_system()) '
                   'WITH CHECK (user_id = app_user_id() OR app_is_system())', t || '_tenant', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE webauthn_challenges;
DROP TABLE passkeys;
DROP TABLE sessions;
DROP TABLE magic_links;
DROP TABLE invitations;
