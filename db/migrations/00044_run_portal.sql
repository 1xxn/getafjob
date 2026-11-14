-- Portal of a run (#514): LinkedIn and Indeed runs share the user's session cookies, so a user gets at most one
-- active run per portal and a fixed gap between two leases, with or without GETAFJOB_LIMITS.

-- +goose Up
ALTER TABLE runs ADD COLUMN portal text NOT NULL DEFAULT '' CHECK (portal IN ('', 'linkedin', 'indeed'));
CREATE INDEX runs_user_portal ON runs (user_id, portal, leased_at) WHERE portal <> '';

-- +goose Down
DROP INDEX runs_user_portal;
ALTER TABLE runs DROP COLUMN portal;
