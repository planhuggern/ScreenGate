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
		if _, err := tx.Exec("INSERT INTO user_policy_versions (user, policy_version) VALUES (?, 1)", user); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if currentQuota != quota {
		if _, err := tx.Exec("UPDATE user_quotas SET daily_quota_seconds = ? WHERE user = ?", quota, user); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO user_policy_versions (user, policy_version) VALUES (?, 1)
			ON CONFLICT(user) DO UPDATE SET policy_version = policy_version + 1`, user); err != nil {
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
	_, err := r.db.Exec(`INSERT INTO heartbeats (reported_at, device_id, user)
		VALUES (?, ?, ?)`, h.ReportedAt.Format(time.RFC3339Nano), h.DeviceID, h.User)
	return err
}

func (r *repository) heartbeatsForUser(user string) ([]recordedHeartbeat, error) {
	rows, err := r.db.Query(`SELECT device_id, reported_at FROM heartbeats WHERE user = ?`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var heartbeats []recordedHeartbeat
	for rows.Next() {
		var deviceID, reportedAt string
		if err := rows.Scan(&deviceID, &reportedAt); err != nil {
			return nil, err
		}
		timestamp, err := time.Parse(time.RFC3339Nano, reportedAt)
		if err != nil {
			return nil, err
		}
		heartbeats = append(heartbeats, recordedHeartbeat{deviceID: deviceID, reportedAt: timestamp})
	}
	return heartbeats, rows.Err()
}

func (r *repository) activities() ([]activity, error) {
	rows, err := r.db.Query(`WITH known_users AS (
			SELECT user FROM heartbeats
			UNION
			SELECT user FROM user_quotas
		)
		SELECT u.user, COALESCE(MAX(h.reported_at), ''), COALESCE(q.daily_quota_seconds, 0)
		FROM known_users u
		LEFT JOIN heartbeats h ON h.user = u.user
		LEFT JOIN user_quotas q ON q.user = u.user
		GROUP BY u.user, q.daily_quota_seconds
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
