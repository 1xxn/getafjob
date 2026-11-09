-- +goose Up
-- Add salary fields and discovery indexes to jobs table (#141)
ALTER TABLE jobs ADD COLUMN salary_min integer;
ALTER TABLE jobs ADD COLUMN salary_max integer;
ALTER TABLE jobs ADD COLUMN salary_currency text NOT NULL DEFAULT 'EUR';

-- Performance indexes for job discovery: freshness, salary, sorting, location
CREATE INDEX jobs_posted_idx ON jobs (posted DESC NULLS LAST, first_seen DESC) WHERE status = 'open';
CREATE INDEX jobs_salary_idx ON jobs (salary_max DESC NULLS LAST, salary_min DESC NULLS LAST) WHERE status = 'open';
CREATE INDEX jobs_company_idx ON jobs (lower(company) ASC) WHERE status = 'open';
CREATE INDEX jobs_location_idx ON jobs (location) WHERE status = 'open';

-- User employer & keyword blocklist with row-level security (#141)
CREATE TABLE user_blocklist (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('company', 'keyword')),
    pattern    text NOT NULL CHECK (length(trim(pattern)) >= 1 AND length(pattern) <= 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, kind, pattern)
);

CREATE INDEX user_blocklist_user_idx ON user_blocklist (user_id, kind);
CREATE INDEX user_blocklist_pattern_idx ON user_blocklist (user_id, pattern);

ALTER TABLE user_blocklist ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_blocklist FORCE ROW LEVEL SECURITY;

CREATE POLICY user_blocklist_tenant ON user_blocklist
    USING (user_id = app_user_id() OR app_is_system())
    WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE user_blocklist;
DROP INDEX IF EXISTS jobs_location_idx;
DROP INDEX IF EXISTS jobs_company_idx;
DROP INDEX IF EXISTS jobs_salary_idx;
DROP INDEX IF EXISTS jobs_posted_idx;
ALTER TABLE jobs DROP COLUMN IF EXISTS salary_currency;
ALTER TABLE jobs DROP COLUMN IF EXISTS salary_max;
ALTER TABLE jobs DROP COLUMN IF EXISTS salary_min;
