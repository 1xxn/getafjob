-- Seal the mail data of tracking suggestions (#578): sender, subject and Message-ID come from the user's private
-- mailbox and are sealed with the user's data key. Expand step: the plain columns stay until the worker has sealed
-- every old row; a later migration drops them.

-- +goose Up
ALTER TABLE tracking_suggestions ADD COLUMN sealed bytea CHECK (sealed IS NULL OR length(sealed) <= 16384);

-- +goose Down
ALTER TABLE tracking_suggestions DROP COLUMN sealed;
