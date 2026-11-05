-- Session rotation: a session gets a new token now and then; the old one stays valid for a short grace period.
-- All tokens of one sign-in share a family, so signing out ends them together.

-- +goose Up
ALTER TABLE sessions
  ADD COLUMN family uuid NOT NULL DEFAULT uuidv7(),
  ADD COLUMN rotated_at timestamptz,
  ADD COLUMN replaced_at timestamptz;
CREATE INDEX sessions_family ON sessions (family);

-- +goose Down
ALTER TABLE sessions DROP COLUMN replaced_at, DROP COLUMN rotated_at, DROP COLUMN family;
