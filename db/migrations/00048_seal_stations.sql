-- Seal career stations (#559): each position and degree is one record sealed with the user's data key. Expand step:
-- the plain columns stay until every row is sealed (the worker seals old rows); a later migration drops them.

-- +goose Up
ALTER TABLE work_experiences ADD COLUMN sealed bytea CHECK (sealed IS NULL OR length(sealed) <= 1048576);
ALTER TABLE educations ADD COLUMN sealed bytea CHECK (sealed IS NULL OR length(sealed) <= 16384);

-- +goose Down
ALTER TABLE educations DROP COLUMN sealed;
ALTER TABLE work_experiences DROP COLUMN sealed;
