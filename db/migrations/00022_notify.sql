-- Notification preferences and channels (#38).
-- Supports Web Push, Mail (SMTP), ntfy and scheduled daily summary.

-- +goose Up
CREATE TABLE notify_preferences (
    user_id uuid NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    email_enabled boolean NOT NULL DEFAULT true,
    email_address text NOT NULL DEFAULT '',
    webpush_enabled boolean NOT NULL DEFAULT true,
    ntfy_enabled boolean NOT NULL DEFAULT false,
    ntfy_url text NOT NULL DEFAULT 'https://ntfy.sh',
    ntfy_topic text NOT NULL DEFAULT '',
    ntfy_token text NOT NULL DEFAULT '',
    daily_summary_enabled boolean NOT NULL DEFAULT true,
    daily_summary_time text NOT NULL DEFAULT '18:00',
    last_summary_at timestamptz,
    quiet_hours_enabled boolean NOT NULL DEFAULT false,
    quiet_hours_start text NOT NULL DEFAULT '22:00',
    quiet_hours_end text NOT NULL DEFAULT '08:00',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

ALTER TABLE notify_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE notify_preferences FORCE ROW LEVEL SECURITY;

CREATE POLICY notify_preferences_tenant ON notify_preferences
    USING (user_id = app_user_id() OR app_is_system())
    WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE notify_preferences;
