-- +goose Up
ALTER TABLE heartbeats ADD COLUMN usage_batch_id TEXT;
CREATE TABLE usage_batches (
    user TEXT NOT NULL,
    device_id TEXT NOT NULL,
    batch_id TEXT NOT NULL,
    payload_hash TEXT NOT NULL,
    PRIMARY KEY(user, device_id, batch_id)
);
CREATE TABLE usage_intervals (
    id INTEGER PRIMARY KEY,
    user TEXT NOT NULL,
    device_id TEXT NOT NULL,
    received_at TEXT NOT NULL,
    received_unix INTEGER NOT NULL,
    start_unix INTEGER NOT NULL,
    end_at TEXT NOT NULL,
    credited_milliseconds INTEGER NOT NULL
);
CREATE INDEX usage_intervals_user_time ON usage_intervals(user, start_unix);

-- +goose Down
DROP TABLE usage_intervals;
DROP TABLE usage_batches;
ALTER TABLE heartbeats DROP COLUMN usage_batch_id;
