-- +goose Up
CREATE TABLE monitor_devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user TEXT NOT NULL,
    device_id TEXT NOT NULL,
    received_at TEXT NOT NULL,
    session_state TEXT NOT NULL,
    client_version TEXT NOT NULL,
    http_status INTEGER NOT NULL,
    UNIQUE(user, device_id)
);
CREATE TABLE heartbeat_monitor (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    device INTEGER NOT NULL REFERENCES monitor_devices(id),
    received_at TEXT NOT NULL,
    gap_milliseconds INTEGER,
    session_state TEXT NOT NULL,
    client_version TEXT NOT NULL,
    reported_seconds INTEGER,
    credited_milliseconds INTEGER,
    remaining_seconds INTEGER,
    quota_seconds INTEGER,
    lease_seconds INTEGER,
    action TEXT NOT NULL,
    reason TEXT NOT NULL,
    duplicate INTEGER NOT NULL,
    http_status INTEGER NOT NULL
);

-- +goose Down
DROP TABLE heartbeat_monitor;
DROP TABLE monitor_devices;
