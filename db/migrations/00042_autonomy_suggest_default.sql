-- Nothing runs on its own until the user turns it on (ADR 0028): the stored default matches the code.

-- +goose Up
ALTER TABLE autonomy_settings ALTER COLUMN level SET DEFAULT 'suggest';

-- +goose Down
ALTER TABLE autonomy_settings ALTER COLUMN level SET DEFAULT 'approve';
