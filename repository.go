package main

import (
	"database/sql"
	"embed"
	"time"

	"github.com/pressly/goose/v3"
	_ "screengate/migrations"

	_ "modernc.org/sqlite"
)

type repository struct {
	db *sql.DB
}

//go:embed migrations/*.sql
var migrationFiles embed.FS

func openRepository(path string) (*repository, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite has one writer. Keeping one connection also gives in-memory
	// databases and connection-scoped pragmas predictable semantics.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA busy_timeout = 5000", "PRAGMA journal_mode = WAL", "PRAGMA foreign_keys = ON"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	goose.SetBaseFS(migrationFiles)
	if err := goose.SetDialect("sqlite3"); err != nil {
		db.Close()
		return nil, err
	}
	if err := goose.Up(db, "migrations"); err != nil {
		db.Close()
		return nil, err
	}
	return &repository{db: db}, nil
}

func (r *repository) close() error {
	return r.db.Close()
}

func (r *repository) userQuota(user string) (int, error) {
	var quota int
	err := r.db.QueryRow("SELECT daily_quota_seconds FROM user_quotas WHERE user = ?", user).Scan(&quota)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return quota, err
}

func (r *repository) setUserQuota(user string, quota int) error {
	if !validPolicyUser(user) || quota < 0 || quota > 86400 {
		return errInvalidPolicy
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var currentQuota int
	err = tx.QueryRow("SELECT daily_quota_seconds FROM user_quotas WHERE user = ?", user).Scan(&currentQuota)
	if err == sql.ErrNoRows {
		if _, err := tx.Exec("INSERT INTO user_quotas (user, daily_quota_seconds) VALUES (?, ?)", user, quota); err != nil {
			return err
		}
		if err := bumpPolicyVersion(tx, user); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if currentQuota != quota {
		if _, err := tx.Exec("UPDATE user_quotas SET daily_quota_seconds = ? WHERE user = ?", quota, user); err != nil {
			return err
		}
		if err := bumpPolicyVersion(tx, user); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *repository) userPolicyVersion(user string) (int, error) {
	var version int
	err := r.db.QueryRow("SELECT policy_version FROM user_policy_versions WHERE user = ?", user).Scan(&version)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return version, err
}

func (r *repository) addHeartbeat(h heartbeat) error {
	return r.addHeartbeatInLocation(h, time.Local)
}

func (r *repository) addHeartbeatInLocation(h heartbeat, location *time.Location) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var requestID any
	var credited any
	var creditedUntil any
	accountingTime := h.ReportedAt
	available := int64(0)
	if h.HeartbeatID != "" {
		requestID = h.HeartbeatID
		var existing int
		err := tx.QueryRow(`SELECT 1 FROM heartbeats WHERE user = ? AND device_id = ? AND heartbeat_id = ?`, h.User, h.DeviceID, h.HeartbeatID).Scan(&existing)
		if err == nil {
			return tx.Commit()
		}
		if err != sql.ErrNoRows {
			return err
		}
		var previous string
		var firstReported int64
		err = tx.QueryRow(`SELECT reported_at, first_reported_unix, available_milliseconds FROM user_presence WHERE user = ? AND device_id = ?`, h.User, h.DeviceID).Scan(&previous, &firstReported, &available)
		milliseconds := int64(0)
		until := h.ReportedAt
		if err == nil {
			at, err := time.Parse(time.RFC3339Nano, previous)
			if err != nil {
				return err
			}
			// Keep unused elapsed time: reports queued during network transit
			// can arrive back-to-back after acknowledgement and still represent
			// real usage. The balance bounds cumulative credit by server time.
			available = min(maxReportedActivity.Milliseconds(), available+max(0, h.ReportedAt.Sub(at).Milliseconds()))
			milliseconds = min(int64(h.ActiveSeconds)*1000, available)
			available -= milliseconds
			if h.ActivityDate != "" {
				day, err := time.ParseInLocation("2006-01-02", h.ActivityDate, location)
				if err != nil {
					return errInvalidHeartbeat
				}
				dayEnd := day.AddDate(0, 0, 1)
				if firstReported >= dayEnd.Unix() && milliseconds > 0 {
					return errInvalidHeartbeat
				}
				if dayEnd.Before(until) {
					until = dayEnd
				}
			}
		} else if err != sql.ErrNoRows {
			return err
		}
		credited = milliseconds
		creditedUntil = until.UTC().Format(time.RFC3339Nano)
		accountingTime = until
	}
	state := h.SessionState
	if state == "" {
		state = "active"
	}
	_, err = tx.Exec(`INSERT INTO heartbeats (reported_at, reported_unix, accounting_unix, device_id, user, heartbeat_id, credited_milliseconds, credited_until, session_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, h.ReportedAt.UTC().Format(time.RFC3339Nano), h.ReportedAt.Unix(), accountingTime.Unix(), h.DeviceID, h.User, requestID, credited, creditedUntil, state)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO user_presence (user, device_id, reported_at, reported_unix, first_reported_unix, available_milliseconds, session_state) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user, device_id) DO UPDATE SET reported_at = excluded.reported_at, reported_unix = excluded.reported_unix, available_milliseconds = excluded.available_milliseconds, session_state = excluded.session_state
		WHERE excluded.reported_unix >= user_presence.reported_unix`, h.User, h.DeviceID, h.ReportedAt.UTC().Format(time.RFC3339Nano), h.ReportedAt.Unix(), h.ReportedAt.Unix(), available, state)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *repository) heartbeatsForUser(user string) ([]recordedHeartbeat, error) {
	rows, err := r.db.Query(`SELECT device_id, reported_at, credited_milliseconds, credited_until, session_state FROM heartbeats WHERE user = ? ORDER BY reported_unix, id`, user)
	if err != nil {
		return nil, err
	}
	return scanHeartbeats(rows)
}

func (r *repository) heartbeatsForUserRange(user string, start, end time.Time) ([]recordedHeartbeat, error) {
	// Query by the time usage belongs to, not when an offline report arrived.
	// Even a report retried weeks later stays in the correct day, and the
	// indexed query is bounded independently of total database history.
	rows, err := r.db.Query(`SELECT device_id, reported_at, credited_milliseconds, credited_until, session_state FROM heartbeats
		WHERE user = ? AND accounting_unix >= ? AND accounting_unix <= ? ORDER BY reported_unix, id`,
		user, start.Add(-maxHeartbeatGap).Unix(), end.Add(maxReportedActivity).Unix())
	if err != nil {
		return nil, err
	}
	return scanHeartbeats(rows)
}

func scanHeartbeats(rows *sql.Rows) ([]recordedHeartbeat, error) {
	defer rows.Close()

	var heartbeats []recordedHeartbeat
	for rows.Next() {
		var deviceID, reportedAt string
		var credited sql.NullInt64
		var creditedUntil sql.NullString
		var state string
		if err := rows.Scan(&deviceID, &reportedAt, &credited, &creditedUntil, &state); err != nil {
			return nil, err
		}
		timestamp, err := time.Parse(time.RFC3339Nano, reportedAt)
		if err != nil {
			return nil, err
		}
		sample := recordedHeartbeat{deviceID: deviceID, reportedAt: timestamp, sessionState: state}
		if credited.Valid {
			value := credited.Int64
			sample.creditedMilliseconds = &value
			if creditedUntil.Valid {
				sample.creditedUntil, err = time.Parse(time.RFC3339Nano, creditedUntil.String)
				if err != nil {
					return nil, err
				}
			}
		}
		heartbeats = append(heartbeats, sample)
	}
	return heartbeats, rows.Err()
}

func (r *repository) activities() ([]activity, error) {
	rows, err := r.db.Query(`WITH known_users AS (
			SELECT user FROM user_presence
			UNION
			SELECT user FROM user_quotas
			UNION SELECT user FROM user_settings
			UNION SELECT user FROM weekday_policies
			UNION SELECT user FROM daily_bonuses
		)
		SELECT u.user, COALESCE((SELECT p.reported_at FROM user_presence p WHERE p.user = u.user ORDER BY p.reported_unix DESC LIMIT 1), ''), COALESCE(q.daily_quota_seconds, 0)
		FROM known_users u
		LEFT JOIN user_quotas q ON q.user = u.user
		ORDER BY u.user`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var activities []activity
	for rows.Next() {
		var item activity
		if err := rows.Scan(&item.User, &item.LastReportedAt, &item.QuotaSeconds); err != nil {
			return nil, err
		}
		activities = append(activities, item)
	}
	return activities, rows.Err()
}
