-- Last activity per account (#296), so the admin list keeps it after the sessions end. A trigger copies every new
-- or touched session time to the account (sign-in, rotation and touch alike).

-- +goose Up
ALTER TABLE users ADD COLUMN last_seen_at timestamptz;
UPDATE users u SET last_seen_at = s.at FROM (SELECT user_id, max(last_seen_at) AS at FROM sessions GROUP BY user_id) s
WHERE s.user_id = u.id;

-- +goose StatementBegin
CREATE FUNCTION user_last_seen() RETURNS trigger
  LANGUAGE plpgsql
  SET search_path = pg_catalog, public
AS $$
BEGIN
  UPDATE users SET last_seen_at = GREATEST(last_seen_at, NEW.last_seen_at) WHERE id = NEW.user_id;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER sessions_user_last_seen AFTER INSERT OR UPDATE OF last_seen_at ON sessions
  FOR EACH ROW EXECUTE FUNCTION user_last_seen();

-- +goose Down
DROP TRIGGER IF EXISTS sessions_user_last_seen ON sessions;
DROP FUNCTION IF EXISTS user_last_seen();
ALTER TABLE users DROP COLUMN last_seen_at;
