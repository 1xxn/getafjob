-- Finished backups (#261), recorded by deploy/backup.sh through `admin backup-done`; the admin system page shows the
-- last one. Only system transactions see the table.

-- +goose Up
CREATE TABLE backups (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  finished_at timestamptz NOT NULL DEFAULT now(),
  bytes       bigint NOT NULL CHECK (bytes >= 0)
);

ALTER TABLE backups ENABLE ROW LEVEL SECURITY;
ALTER TABLE backups FORCE ROW LEVEL SECURITY;

CREATE POLICY backups_system ON backups
  USING (app_is_system())
  WITH CHECK (app_is_system());

-- +goose Down
DROP TABLE backups;
