-- Tenant isolation (ADR 0006): serve and worker connect as getafjob_app, which cannot bypass row-level security.
-- Every user table carries user_id; policies compare it with the per-transaction setting app.user_id.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'getafjob_app') THEN
    CREATE ROLE getafjob_app NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
  END IF;
END
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA public TO getafjob_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO getafjob_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO getafjob_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO getafjob_app;
-- Readiness checks compare the schema version; the application role may read it, never change it.
GRANT SELECT ON goose_db_version TO getafjob_app;

CREATE FUNCTION app_user_id() RETURNS uuid LANGUAGE sql STABLE
  AS $$ SELECT nullif(current_setting('app.user_id', true), '')::uuid $$;

-- System context: explicit, reviewed code paths only (global job index, admin tasks, sign-in lookups).
CREATE FUNCTION app_is_system() RETURNS boolean LANGUAGE sql STABLE
  AS $$ SELECT coalesce(current_setting('app.system', true), '') = 'on' $$;

CREATE TABLE users (
  id           uuid PRIMARY KEY DEFAULT uuidv7(),
  email        citext NOT NULL UNIQUE CHECK (length(email) <= 254),
  display_name text NOT NULL DEFAULT '' CHECK (length(display_name) <= 200),
  role         text NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
  locale       text NOT NULL DEFAULT 'en' CHECK (locale IN ('en', 'de')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  disabled_at  timestamptz
);
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
CREATE POLICY users_tenant ON users
  USING (id = app_user_id() OR app_is_system())
  WITH CHECK (id = app_user_id() OR app_is_system());

-- Append-only audit log; codes plus params, no personal data in params.
CREATE TABLE audit_events (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id    uuid REFERENCES users (id) ON DELETE CASCADE,
  actor      text NOT NULL CHECK (actor IN ('user', 'admin', 'system')),
  code       text NOT NULL CHECK (code ~ '^[a-z][a-z0-9_.]{1,63}$'),
  params     jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_user_created ON audit_events (user_id, created_at DESC);
ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_events FORCE ROW LEVEL SECURITY;
CREATE POLICY audit_events_tenant ON audit_events
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());
REVOKE UPDATE, DELETE ON audit_events FROM getafjob_app;

-- +goose Down
REVOKE SELECT ON goose_db_version FROM getafjob_app;
DROP TABLE audit_events;
DROP TABLE users;
DROP FUNCTION app_is_system();
DROP FUNCTION app_user_id();
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE EXECUTE ON FUNCTIONS FROM getafjob_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE USAGE, SELECT ON SEQUENCES FROM getafjob_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM getafjob_app;
REVOKE USAGE ON SCHEMA public FROM getafjob_app;
