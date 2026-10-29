-- +goose Up
ALTER TABLE monitor_devices ADD COLUMN session_diagnostics TEXT NOT NULL DEFAULT '';
ALTER TABLE heartbeat_monitor ADD COLUMN session_diagnostics TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE heartbeat_monitor DROP COLUMN session_diagnostics;
ALTER TABLE monitor_devices DROP COLUMN session_diagnostics;
