-- Career Stations (#138): structured work experiences and educations with tenant isolation (RLS) and sealed bullets.

-- +goose Up
CREATE TABLE work_experiences (
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  company        text NOT NULL DEFAULT '' CHECK (length(company) <= 200),
  title          text NOT NULL DEFAULT '' CHECK (length(title) <= 200),
  location       text NOT NULL DEFAULT '' CHECK (length(location) <= 200),
  workplace_type text NOT NULL DEFAULT '' CHECK (workplace_type IN ('', 'remote', 'hybrid', 'onsite')),
  start_date     text NOT NULL DEFAULT '' CHECK (length(start_date) <= 30),
  end_date       text NOT NULL DEFAULT '' CHECK (length(end_date) <= 30),
  is_current     boolean NOT NULL DEFAULT false,
  sealed_bullets bytea CHECK (sealed_bullets IS NULL OR length(sealed_bullets) <= 65536),
  sort_order     integer NOT NULL DEFAULT 0,
  created_at     timestamptz NOT NULL,
  updated_at     timestamptz NOT NULL
);
CREATE INDEX work_experiences_user ON work_experiences (user_id, sort_order, id);
ALTER TABLE work_experiences ENABLE ROW LEVEL SECURITY;
ALTER TABLE work_experiences FORCE ROW LEVEL SECURITY;
CREATE POLICY work_experiences_tenant ON work_experiences
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

CREATE TABLE educations (
  id             uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  institution    text NOT NULL DEFAULT '' CHECK (length(institution) <= 200),
  degree         text NOT NULL DEFAULT '' CHECK (length(degree) <= 200),
  field_of_study text NOT NULL DEFAULT '' CHECK (length(field_of_study) <= 200),
  start_date     text NOT NULL DEFAULT '' CHECK (length(start_date) <= 30),
  end_date       text NOT NULL DEFAULT '' CHECK (length(end_date) <= 30),
  is_current     boolean NOT NULL DEFAULT false,
  grade          text NOT NULL DEFAULT '' CHECK (length(grade) <= 50),
  sort_order     integer NOT NULL DEFAULT 0,
  created_at     timestamptz NOT NULL,
  updated_at     timestamptz NOT NULL
);
CREATE INDEX educations_user ON educations (user_id, sort_order, id);
ALTER TABLE educations ENABLE ROW LEVEL SECURITY;
ALTER TABLE educations FORCE ROW LEVEL SECURITY;
CREATE POLICY educations_tenant ON educations
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE educations;
DROP TABLE work_experiences;
