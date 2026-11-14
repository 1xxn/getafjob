-- Maintenance mode (#518): one row while the instance is in maintenance, none otherwise. Set by an admin on the
-- system page or with `admin maintenance`; serve, worker and gateway read it. Only system transactions see it.

-- +goose Up
CREATE TABLE maintenance (
  id      boolean PRIMARY KEY DEFAULT true CHECK (id),
  message text NOT NULL DEFAULT '' CHECK (char_length(message) <= 200),
  since   timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE maintenance ENABLE ROW LEVEL SECURITY;
ALTER TABLE maintenance FORCE ROW LEVEL SECURITY;

CREATE POLICY maintenance_system ON maintenance
  USING (app_is_system())
  WITH CHECK (app_is_system());

-- +goose Down
DROP TABLE maintenance;
