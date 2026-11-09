-- +goose Up
ALTER TABLE user_settings ADD COLUMN warning_only INTEGER NOT NULL DEFAULT 0 CHECK (warning_only IN (0, 1));

-- +goose Down
ALTER TABLE user_settings DROP COLUMN warning_only;
