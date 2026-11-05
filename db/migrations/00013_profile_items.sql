-- Answer bank and text blocks (#15): sealed with the user's data key, matched in memory.

-- +goose Up
CREATE TABLE profile_items (
  id         uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind       text NOT NULL CHECK (kind IN ('answer', 'text')),
  sealed     bytea NOT NULL CHECK (length(sealed) <= 65536),
  updated_at timestamptz NOT NULL
);
CREATE INDEX profile_items_user ON profile_items (user_id, kind, id);
ALTER TABLE profile_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE profile_items FORCE ROW LEVEL SECURITY;
CREATE POLICY profile_items_tenant ON profile_items
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE profile_items;
