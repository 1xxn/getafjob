-- When a user last marked the activity as read (#185); newer entries count as unread.

-- +goose Up
CREATE TABLE notify_reads (
  user_id uuid NOT NULL PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  seen_at timestamptz NOT NULL
);

ALTER TABLE notify_reads ENABLE ROW LEVEL SECURITY;
ALTER TABLE notify_reads FORCE ROW LEVEL SECURITY;

CREATE POLICY notify_reads_tenant ON notify_reads
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- The activity feed reads a user's events newest first.
CREATE INDEX application_events_user_at ON application_events (user_id, at DESC);

-- +goose Down
DROP INDEX application_events_user_at;
DROP TABLE notify_reads;
