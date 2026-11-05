-- Documents (#15): CVs, certificates and other files of a user. The file lives sealed in the blob store; the name
-- is sealed here. A CV is also a CV variant: label, language and focus words pick it for a job.

-- +goose Up
CREATE TABLE documents (
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind        text NOT NULL CHECK (kind IN ('cv', 'certificate', 'other')),
  sealed_name bytea NOT NULL CHECK (length(sealed_name) <= 2048),
  mime        text NOT NULL CHECK (mime IN ('application/pdf', 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', 'image/png', 'image/jpeg')),
  size        integer NOT NULL CHECK (size > 0),
  label       text NOT NULL DEFAULT '' CHECK (length(label) <= 100),
  language    text NOT NULL DEFAULT '' CHECK (language IN ('', 'en', 'de')),
  focus       text[] NOT NULL DEFAULT '{}' CHECK (cardinality(focus) <= 30),
  created_at  timestamptz NOT NULL
);
CREATE INDEX documents_user ON documents (user_id, created_at);
ALTER TABLE documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE documents FORCE ROW LEVEL SECURITY;
CREATE POLICY documents_tenant ON documents
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE documents;
