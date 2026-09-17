-- +goose Up
CREATE TABLE devices (
    id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL,
    user TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    last_seen INTEGER,
    revoked INTEGER NOT NULL DEFAULT 0 CHECK (revoked IN (0, 1))
);
CREATE INDEX devices_user ON devices(user);
CREATE TABLE pairing_codes (
    code_hash TEXT PRIMARY KEY,
    user TEXT NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE TABLE audit_events (
    id INTEGER PRIMARY KEY,
    occurred_at INTEGER NOT NULL,
    user TEXT NOT NULL,
    action TEXT NOT NULL,
    detail TEXT NOT NULL
);
CREATE INDEX audit_events_time ON audit_events(occurred_at);

-- +goose Down
DROP TABLE audit_events;
DROP TABLE pairing_codes;
DROP TABLE devices;
