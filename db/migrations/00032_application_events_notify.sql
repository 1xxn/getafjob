-- Tell the web server about application changes for the live event stream (#166). The payload holds ids and
-- states only, never personal data; a notification reaches only servers listening on the channel.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION notify_application_event() RETURNS trigger
  LANGUAGE plpgsql
  SET search_path = pg_catalog
AS $$
BEGIN
  PERFORM pg_notify('getafjob_events', json_build_object(
    'user', NEW.user_id, 'app', NEW.application_id, 'event', NEW.event, 'from', NEW.from_state, 'to', NEW.to_state
  )::text);
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER application_events_notify AFTER INSERT ON application_events
  FOR EACH ROW EXECUTE FUNCTION notify_application_event();

-- +goose Down
DROP TRIGGER IF EXISTS application_events_notify ON application_events;
DROP FUNCTION IF EXISTS notify_application_event();
