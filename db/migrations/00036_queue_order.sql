-- The user orders and pauses queued runs (#223). queue_pos is set when an application enters the queue (the end of
-- the queue), the lease takes the lowest position that is not paused. A change of order or pause reaches open pages
-- through the event stream; the payload holds ids only.

-- +goose Up
ALTER TABLE applications
  ADD COLUMN queue_pos bigint,
  ADD COLUMN queue_paused boolean NOT NULL DEFAULT false;

UPDATE applications SET queue_pos = (extract(epoch FROM updated_at) * 1000000)::bigint WHERE state = 'queued';

CREATE INDEX applications_queue ON applications (queue_pos, id) WHERE state = 'queued';

-- +goose StatementBegin
CREATE FUNCTION applications_enqueue() RETURNS trigger
  LANGUAGE plpgsql
  SET search_path = pg_catalog
AS $$
BEGIN
  IF NEW.state = 'queued' AND (TG_OP = 'INSERT' OR OLD.state IS DISTINCT FROM 'queued') THEN
    NEW.queue_pos := (extract(epoch FROM clock_timestamp()) * 1000000)::bigint;
    NEW.queue_paused := false;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER applications_enqueue BEFORE INSERT OR UPDATE OF state ON applications
  FOR EACH ROW EXECUTE FUNCTION applications_enqueue();

-- +goose StatementBegin
CREATE FUNCTION notify_queue_change() RETURNS trigger
  LANGUAGE plpgsql
  SET search_path = pg_catalog
AS $$
BEGIN
  PERFORM pg_notify('getafjob_events', json_build_object(
    'user', NEW.user_id, 'topic', 'queue', 'app', NEW.id, 'event', 'queue'
  )::text);
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER applications_queue_notify AFTER UPDATE OF queue_pos, queue_paused ON applications
  FOR EACH ROW WHEN (NEW.state = 'queued' AND (OLD.queue_pos IS DISTINCT FROM NEW.queue_pos OR OLD.queue_paused IS DISTINCT FROM NEW.queue_paused))
  EXECUTE FUNCTION notify_queue_change();

-- +goose Down
DROP TRIGGER IF EXISTS applications_queue_notify ON applications;
DROP FUNCTION IF EXISTS notify_queue_change();
DROP TRIGGER IF EXISTS applications_enqueue ON applications;
DROP FUNCTION IF EXISTS applications_enqueue();
DROP INDEX IF EXISTS applications_queue;
ALTER TABLE applications DROP COLUMN queue_paused, DROP COLUMN queue_pos;
