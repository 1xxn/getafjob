-- Blocklist patterns are words, not LIKE patterns: like_literal escapes %, _ and the escape character, so "_" or
-- "100%" match only themselves.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION like_literal(s text) RETURNS text
  LANGUAGE sql IMMUTABLE PARALLEL SAFE
  SET search_path = pg_catalog
AS $$ SELECT replace(replace(replace(s, '\', '\\'), '%', '\%'), '_', '\_') $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS like_literal(text);
