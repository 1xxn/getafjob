-- Per-event email opt-in flags and 15-minute email bundling tables (#515).

-- +goose Up
ALTER TABLE notify_preferences
  ADD COLUMN email_needs_input boolean NOT NULL DEFAULT false,
  ADD COLUMN email_approvals boolean NOT NULL DEFAULT false,
  ADD COLUMN email_search_alerts boolean NOT NULL DEFAULT false,
  ADD COLUMN email_daily_summary boolean NOT NULL DEFAULT false;

UPDATE notify_preferences
SET email_needs_input = true,
    email_approvals = true,
    email_search_alerts = true,
    email_daily_summary = daily_summary_enabled
WHERE email_enabled = true AND created_at <> updated_at;

CREATE TABLE notify_email_pending (
    id bigserial PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL,
    title text NOT NULL,
    body text NOT NULL,
    url text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL
);

CREATE INDEX notify_email_pending_due_idx ON notify_email_pending (user_id, kind, created_at);

ALTER TABLE notify_email_pending ENABLE ROW LEVEL SECURITY;
ALTER TABLE notify_email_pending FORCE ROW LEVEL SECURITY;

CREATE POLICY notify_email_pending_tenant ON notify_email_pending
    USING (user_id = app_user_id() OR app_is_system())
    WITH CHECK (user_id = app_user_id() OR app_is_system());

CREATE TABLE notify_email_sent (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL,
    sent_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, kind)
);

ALTER TABLE notify_email_sent ENABLE ROW LEVEL SECURITY;
ALTER TABLE notify_email_sent FORCE ROW LEVEL SECURITY;

CREATE POLICY notify_email_sent_tenant ON notify_email_sent
    USING (user_id = app_user_id() OR app_is_system())
    WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE notify_email_sent;
DROP TABLE notify_email_pending;
ALTER TABLE notify_preferences
  DROP COLUMN email_daily_summary,
  DROP COLUMN email_search_alerts,
  DROP COLUMN email_approvals,
  DROP COLUMN email_needs_input;
