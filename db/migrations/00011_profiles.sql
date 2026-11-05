-- Profiles (#15): the applicant profile sealed with the user's data key; preferences readable for search.

-- +goose Up
CREATE TABLE profiles (
  user_id     uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  sealed      bytea CHECK (length(sealed) <= 1048576),
  preferences jsonb,
  version     bigint NOT NULL DEFAULT 0,
  updated_at  timestamptz NOT NULL
);
ALTER TABLE profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE profiles FORCE ROW LEVEL SECURITY;
CREATE POLICY profiles_tenant ON profiles
  USING (user_id = app_user_id() OR app_is_system())
  WITH CHECK (user_id = app_user_id() OR app_is_system());

-- +goose Down
DROP TABLE profiles;
