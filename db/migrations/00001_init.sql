-- Basics: trigram search for company and job titles (full-text search arrives with the tables).

-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- +goose Down
DROP EXTENSION IF EXISTS pg_trgm;
