-- +goose Up
ALTER TABLE heartbeats ADD COLUMN reported_unix INTEGER NOT NULL DEFAULT 0;
ALTER TABLE heartbeats ADD COLUMN accounting_unix INTEGER NOT NULL DEFAULT 0;
ALTER TABLE heartbeats ADD COLUMN heartbeat_id TEXT;
ALTER TABLE heartbeats ADD COLUMN credited_milliseconds INTEGER;
ALTER TABLE heartbeats ADD COLUMN credited_until TEXT;
ALTER TABLE heartbeats ADD COLUMN session_state TEXT NOT NULL DEFAULT 'active';
UPDATE heartbeats SET reported_unix = CAST(strftime('%s', reported_at) AS INTEGER);
UPDATE heartbeats SET accounting_unix = reported_unix;
CREATE INDEX heartbeats_user_time ON heartbeats(user, reported_unix);
CREATE INDEX heartbeats_user_accounting ON heartbeats(user, accounting_unix);
CREATE UNIQUE INDEX heartbeats_request_id ON heartbeats(user, device_id, heartbeat_id) WHERE heartbeat_id IS NOT NULL;

CREATE TABLE user_settings (
    user TEXT PRIMARY KEY,
    paused INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1))
);
CREATE TABLE weekday_policies (
    user TEXT NOT NULL,
    weekday INTEGER NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    quota_seconds INTEGER NOT NULL CHECK (quota_seconds BETWEEN 0 AND 86400),
    disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    start_minute INTEGER NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
    end_minute INTEGER NOT NULL CHECK (end_minute BETWEEN 0 AND 1440),
    PRIMARY KEY (user, weekday)
);
CREATE TABLE daily_bonuses (
    user TEXT NOT NULL,
    date TEXT NOT NULL,
    seconds INTEGER NOT NULL CHECK (seconds BETWEEN 0 AND 86400),
    PRIMARY KEY (user, date)
);
CREATE TABLE user_presence (
    user TEXT NOT NULL,
    device_id TEXT NOT NULL,
    reported_at TEXT NOT NULL,
    reported_unix INTEGER NOT NULL,
    first_reported_unix INTEGER NOT NULL,
    available_milliseconds INTEGER NOT NULL DEFAULT 0,
    session_state TEXT NOT NULL DEFAULT 'active',
    PRIMARY KEY (user, device_id)
);
INSERT INTO user_presence (user, device_id, reported_at, reported_unix, first_reported_unix, session_state)
SELECT h.user, h.device_id, h.reported_at, h.reported_unix,
    (SELECT MIN(h3.reported_unix) FROM heartbeats h3 WHERE h3.user = h.user AND h3.device_id = h.device_id), h.session_state
FROM heartbeats h
WHERE h.id = (SELECT h2.id FROM heartbeats h2
    WHERE h2.user = h.user AND h2.device_id = h.device_id
    ORDER BY h2.reported_unix DESC, h2.id DESC LIMIT 1);
CREATE INDEX user_presence_time ON user_presence(user, reported_unix);

-- +goose Down
DROP TABLE user_presence;
DROP TABLE daily_bonuses;
DROP TABLE weekday_policies;
DROP TABLE user_settings;
DROP INDEX heartbeats_request_id;
DROP INDEX heartbeats_user_time;
DROP INDEX heartbeats_user_accounting;
ALTER TABLE heartbeats DROP COLUMN session_state;
ALTER TABLE heartbeats DROP COLUMN credited_milliseconds;
ALTER TABLE heartbeats DROP COLUMN credited_until;
ALTER TABLE heartbeats DROP COLUMN heartbeat_id;
ALTER TABLE heartbeats DROP COLUMN reported_unix;
ALTER TABLE heartbeats DROP COLUMN accounting_unix;
