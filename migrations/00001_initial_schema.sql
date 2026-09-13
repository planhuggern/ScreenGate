-- +goose Up
CREATE TABLE IF NOT EXISTS heartbeats (
    id INTEGER PRIMARY KEY,
    reported_at TEXT NOT NULL,
    device_id TEXT NOT NULL,
    user TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS user_quotas (
    user TEXT PRIMARY KEY,
    daily_quota_seconds INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS user_policy_versions (
    user TEXT PRIMARY KEY,
    policy_version INTEGER NOT NULL
);

INSERT OR IGNORE INTO user_policy_versions (user, policy_version)
SELECT user, 1 FROM user_quotas;

-- +goose Down
DROP TABLE IF EXISTS user_policy_versions;
DROP TABLE IF EXISTS user_quotas;
DROP TABLE IF EXISTS heartbeats;
