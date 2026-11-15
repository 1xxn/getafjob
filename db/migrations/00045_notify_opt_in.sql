-- Opt-in email defaults (#515): new accounts and untouched defaults do not send notification emails until enabled.

-- +goose Up
ALTER TABLE notify_preferences
  ALTER COLUMN email_enabled SET DEFAULT false,
  ALTER COLUMN daily_summary_enabled SET DEFAULT false;

UPDATE notify_preferences
SET email_enabled = false, daily_summary_enabled = false
WHERE created_at = updated_at;

-- +goose Down
ALTER TABLE notify_preferences
  ALTER COLUMN email_enabled SET DEFAULT true,
  ALTER COLUMN daily_summary_enabled SET DEFAULT true;
