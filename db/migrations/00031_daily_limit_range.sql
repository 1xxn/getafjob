-- Expand daily_limit ceiling for self-host usage (#156).
-- Removes the artificial 50-limit cap and allows up to 500 applications per day.

-- +goose Up
ALTER TABLE autonomy_settings
  DROP CONSTRAINT IF EXISTS autonomy_settings_daily_limit_check,
  ADD CONSTRAINT autonomy_settings_daily_limit_check CHECK (daily_limit BETWEEN 0 AND 500);

-- +goose Down
ALTER TABLE autonomy_settings
  DROP CONSTRAINT IF EXISTS autonomy_settings_daily_limit_check,
  ADD CONSTRAINT autonomy_settings_daily_limit_check CHECK (daily_limit BETWEEN 0 AND 50);
