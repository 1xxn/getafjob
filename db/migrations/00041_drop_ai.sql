-- The AI module is removed (ADR 0027): its settings and usage tables go.

-- +goose Up
DROP TABLE IF EXISTS ai_usage_events;
DROP TABLE IF EXISTS user_ai_settings;

-- +goose Down
CREATE TABLE user_ai_settings (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled boolean NOT NULL DEFAULT false,
    encrypted_api_key bytea,
    spent_cents integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

ALTER TABLE user_ai_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_ai_settings FORCE ROW LEVEL SECURITY;

CREATE POLICY user_ai_settings_tenant ON user_ai_settings
    USING (user_id = app_user_id() OR app_is_system())
    WITH CHECK (user_id = app_user_id() OR app_is_system());

CREATE TABLE ai_usage_events (
    id bigserial PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    task text NOT NULL CHECK (task IN ('plan', 'draft', 'extract', 'classify')),
    model text NOT NULL,
    tokens_in integer NOT NULL,
    tokens_out integer NOT NULL,
    cost_cents integer NOT NULL,
    created_at timestamptz NOT NULL
);

ALTER TABLE ai_usage_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_usage_events FORCE ROW LEVEL SECURITY;

CREATE POLICY ai_usage_events_tenant ON ai_usage_events
    USING (user_id = app_user_id() OR app_is_system())
    WITH CHECK (user_id = app_user_id() OR app_is_system());

CREATE INDEX ai_usage_events_user_idx ON ai_usage_events (user_id, created_at DESC);
