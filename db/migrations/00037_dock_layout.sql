-- Where the live dock sits and how large it is, per user (#224): "<side>:<size>", empty for the default.

-- +goose Up
ALTER TABLE users ADD COLUMN dock_layout text NOT NULL DEFAULT ''
  CHECK (dock_layout ~ '^((left|right):(sm|md|lg))?$');

-- +goose Down
ALTER TABLE users DROP COLUMN dock_layout;
