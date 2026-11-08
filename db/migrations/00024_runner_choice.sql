-- Runner choice per user (#40): server, home_runner, or extension.
-- Home runner tokens allow users to run their own runner on their machine.

-- +goose Up
ALTER TABLE autonomy_settings
  ADD COLUMN runner_choice text NOT NULL DEFAULT 'server' CHECK (runner_choice IN ('server', 'home_runner', 'extension')),
  ADD COLUMN home_runner_token_hash bytea UNIQUE CHECK (home_runner_token_hash IS NULL OR length(home_runner_token_hash) = 32);

-- +goose Down
ALTER TABLE autonomy_settings
  DROP COLUMN home_runner_token_hash,
  DROP COLUMN runner_choice;
