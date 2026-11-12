-- A status suggestion from the mailbox reaches open pages live (#245): the bell, /activity and the pipeline refresh.
-- The payload holds ids only, never mail content.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION notify_suggestion() RETURNS trigger
  LANGUAGE plpgsql
  SET search_path = pg_catalog
AS $$
BEGIN
  PERFORM pg_notify('getafjob_events', json_build_object(
    'user', NEW.user_id, 'topic', 'suggestion', 'app', NEW.application_id, 'event', 'suggestion'
  )::text);
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER tracking_suggestions_notify AFTER INSERT ON tracking_suggestions
  FOR EACH ROW EXECUTE FUNCTION notify_suggestion();

-- +goose Down
DROP TRIGGER IF EXISTS tracking_suggestions_notify ON tracking_suggestions;
DROP FUNCTION IF EXISTS notify_suggestion();
