-- Tracking mailbox and status suggestions (#37).
-- Users configure IMAP access with an app password; the worker checks new mail
-- read-only (EXAMINE, BODY.PEEK, no flags changed) and records status suggestions.
-- Security codes are strictly ignored and never read.

-- +goose Up
CREATE TABLE tracking_mailboxes (
    user_id uuid NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    host text NOT NULL,
    port integer NOT NULL DEFAULT 993,
    tls boolean NOT NULL DEFAULT true,
    username text NOT NULL,
    encrypted_password bytea NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    last_uid bigint NOT NULL DEFAULT 0,
    uid_validity bigint NOT NULL DEFAULT 0,
    last_checked_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

ALTER TABLE tracking_mailboxes ENABLE ROW LEVEL SECURITY;
ALTER TABLE tracking_mailboxes FORCE ROW LEVEL SECURITY;

CREATE POLICY tracking_mailboxes_tenant ON tracking_mailboxes
    USING (user_id = app_user_id() OR app_is_system())
    WITH CHECK (user_id = app_user_id() OR app_is_system());

CREATE TABLE tracking_suggestions (
    id uuid NOT NULL PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    application_id uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    message_uid bigint NOT NULL,
    message_id text,
    message_from text NOT NULL,
    message_subject text NOT NULL,
    message_date timestamptz NOT NULL,
    category text NOT NULL,
    suggested_state text NOT NULL,
    reasons jsonb NOT NULL DEFAULT '[]',
    status text NOT NULL DEFAULT 'pending',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (user_id, application_id, message_uid)
);

CREATE INDEX tracking_suggestions_user_status ON tracking_suggestions (user_id, status);
CREATE INDEX tracking_suggestions_app ON tracking_suggestions (application_id);

ALTER TABLE tracking_suggestions ENABLE ROW LEVEL SECURITY;
ALTER TABLE tracking_suggestions FORCE ROW LEVEL SECURITY;

CREATE POLICY tracking_suggestions_tenant ON tracking_suggestions
    USING (user_id = app_user_id() OR app_is_system())
    WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE tracking_suggestions;
DROP TABLE tracking_mailboxes;
