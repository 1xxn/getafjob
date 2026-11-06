-- Handoff window (#26): how long a run waits for the user (question, CAPTCHA, code) before it is parked; a
-- notice goes out shortly before.

-- +goose Up
ALTER TABLE applications ADD COLUMN handoff_until timestamptz, ADD COLUMN handoff_warned boolean NOT NULL DEFAULT false;
CREATE INDEX applications_handoff ON applications (handoff_until) WHERE state = 'needs_input';

-- +goose Down
DROP INDEX applications_handoff;
ALTER TABLE applications DROP COLUMN handoff_warned, DROP COLUMN handoff_until;
