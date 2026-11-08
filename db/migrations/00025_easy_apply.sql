-- Easy Apply adapters and portal sessions (#43).
-- Opt-in per user with terms of use / ban risk acceptance.
-- Portal sessions store envelope-encrypted cookies only; passwords are never seen or stored.

-- +goose Up
ALTER TABLE autonomy_settings
  ADD COLUMN easy_apply_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN easy_apply_terms_accepted_at timestamptz;

CREATE TABLE portal_sessions (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    portal text NOT NULL CHECK (portal IN ('linkedin', 'indeed')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'expired', 'disconnected')),
    encrypted_cookies bytea NOT NULL,
    daily_limit integer NOT NULL DEFAULT 5 CHECK (daily_limit > 0 AND daily_limit <= 20),
    last_used_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, portal)
);

ALTER TABLE portal_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE portal_sessions FORCE ROW LEVEL SECURITY;

CREATE POLICY portal_sessions_tenant ON portal_sessions
    USING (user_id = app_user_id() OR app_is_system())
    WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE portal_sessions;
ALTER TABLE autonomy_settings
  DROP COLUMN easy_apply_terms_accepted_at,
  DROP COLUMN easy_apply_enabled;
