-- Planner and pacing indexes (#36, ADR 0016).
-- Fast lookup of user runs for active run checks and pacing intervals,
-- and submit attempts for 24-hour quota counting.

-- +goose Up
CREATE INDEX runs_user_leased ON runs (user_id, leased_at DESC);
CREATE INDEX submit_attempts_user_window ON submit_attempts (user_id, started_at DESC);

-- +goose Down
DROP INDEX submit_attempts_user_window;
DROP INDEX runs_user_leased;
