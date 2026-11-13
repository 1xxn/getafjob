-- Saved job list filters with an optional daily alert (#231). query is the job list's URL query; checked_at is
-- when the alert last looked for new jobs (jobs first seen after it count as new).

-- +goose Up
CREATE TABLE saved_searches (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  query      text NOT NULL CHECK (length(query) <= 2000),
  alert      boolean NOT NULL DEFAULT false,
  checked_at timestamptz NOT NULL DEFAULT now(),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, name)
);

CREATE INDEX saved_searches_due ON saved_searches (checked_at) WHERE alert;

ALTER TABLE saved_searches ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_searches FORCE ROW LEVEL SECURITY;

CREATE POLICY saved_searches_tenant ON saved_searches
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE saved_searches;
