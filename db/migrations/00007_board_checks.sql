-- Board discovery: per company of job-board hits, when its own board was looked for and what came out.

-- +goose Up
CREATE TABLE board_checks (
  company_key text PRIMARY KEY CHECK (length(company_key) BETWEEN 1 AND 300),
  checked_at  timestamptz NOT NULL,
  recheck_at  timestamptz NOT NULL,
  result      text NOT NULL CHECK (result IN ('found', 'none', 'error')),
  board_id    bigint REFERENCES boards (id) ON DELETE SET NULL
);
CREATE INDEX board_checks_recheck ON board_checks (recheck_at);

-- +goose Down
DROP TABLE board_checks;
