package main

import (
	"database/sql"
	"reflect"
	"time"
)

func bumpPolicyVersion(tx *sql.Tx, user string) error {
	_, err := tx.Exec(`INSERT INTO user_policy_versions (user, policy_version) VALUES (?, 1)
		ON CONFLICT(user) DO UPDATE SET policy_version = policy_version + 1`, user)
	return err
}

func (r *repository) userPolicy(user, date string) (userPolicy, error) {
	p := userPolicy{User: user}
	err := r.db.QueryRow(`SELECT COALESCE((SELECT daily_quota_seconds FROM user_quotas WHERE user = ?), 0),
		COALESCE((SELECT paused FROM user_settings WHERE user = ?), 0),
		COALESCE((SELECT seconds FROM daily_bonuses WHERE user = ? AND date = ?), 0),
		COALESCE((SELECT policy_version FROM user_policy_versions WHERE user = ?), 0)`, user, user, user, date, user).
		Scan(&p.DailyQuotaSeconds, &p.Paused, &p.BonusSeconds, &p.PolicyVersion)
	if err != nil {
		return p, err
	}
	p.Weekdays = make([]weekdayPolicy, 7)
	for i := range p.Weekdays {
		p.Weekdays[i] = weekdayPolicy{Weekday: i, QuotaSeconds: p.DailyQuotaSeconds, EndMinute: 1440}
	}
	rows, err := r.db.Query(`SELECT weekday, quota_seconds, disabled, start_minute, end_minute FROM weekday_policies WHERE user = ? ORDER BY weekday`, user)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var day weekdayPolicy
		if err := rows.Scan(&day.Weekday, &day.QuotaSeconds, &day.Disabled, &day.StartMinute, &day.EndMinute); err != nil {
			return p, err
		}
		day.Configured = true
		p.Weekdays[day.Weekday] = day
	}
	return p, rows.Err()
}

func (r *repository) setWeekdayPolicies(user string, days []weekdayPolicy) error {
	if !validPolicyUser(user) {
		return errInvalidPolicy
	}
	if err := validateWeekdayPolicies(days); err != nil {
		return err
	}
	current, err := r.userPolicy(user, "")
	if err != nil {
		return err
	}
	var before, after [7]weekdayPolicy
	for _, day := range current.Weekdays {
		if day.Configured {
			before[day.Weekday] = day
		}
	}
	for _, day := range days {
		if day.Configured {
			after[day.Weekday] = day
		}
	}
	if reflect.DeepEqual(before, after) {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM weekday_policies WHERE user = ?`, user); err != nil {
		return err
	}
	for _, day := range days {
		if !day.Configured {
			continue
		}
		_, err := tx.Exec(`INSERT INTO weekday_policies (user, weekday, quota_seconds, disabled, start_minute, end_minute) VALUES (?, ?, ?, ?, ?, ?)`,
			user, day.Weekday, day.QuotaSeconds, day.Disabled, day.StartMinute, day.EndMinute)
		if err != nil {
			return err
		}
	}
	if err := bumpPolicyVersion(tx, user); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *repository) setUserPaused(user string, paused bool) error {
	if !validPolicyUser(user) {
		return errInvalidPolicy
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current bool
	err = tx.QueryRow(`SELECT paused FROM user_settings WHERE user = ?`, user).Scan(&current)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if current == paused {
		return tx.Commit()
	}
	if _, err := tx.Exec(`INSERT INTO user_settings (user, paused) VALUES (?, ?) ON CONFLICT(user) DO UPDATE SET paused = excluded.paused`, user, paused); err != nil {
		return err
	}
	if err := bumpPolicyVersion(tx, user); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *repository) changeBonus(user, date string, seconds int, clear bool) error {
	if !validPolicyUser(user) || seconds < 0 || seconds > 86400 {
		return errInvalidPolicy
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	err = tx.QueryRow(`SELECT seconds FROM daily_bonuses WHERE user = ? AND date = ?`, user, date).Scan(&current)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	updated := current + seconds
	if clear {
		updated = 0
	}
	if updated > 86400 {
		return errInvalidPolicy
	}
	if updated == current {
		return tx.Commit()
	}
	if _, err := tx.Exec(`INSERT INTO daily_bonuses (user, date, seconds) VALUES (?, ?, ?) ON CONFLICT(user, date) DO UPDATE SET seconds = excluded.seconds`, user, date, updated); err != nil {
		return err
	}
	if err := bumpPolicyVersion(tx, user); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *repository) devicesForUser(user string, now time.Time) ([]deviceActivity, error) {
	rows, err := r.db.Query(`SELECT device_id, reported_at, session_state FROM user_presence WHERE user = ? ORDER BY device_id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var devices []deviceActivity
	for rows.Next() {
		var device deviceActivity
		if err := rows.Scan(&device.ID, &device.LastReportedAt, &device.State); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, device.LastReportedAt)
		if err != nil {
			return nil, err
		}
		device.Online = !at.After(now.Add(5*time.Second)) && now.Sub(at) <= 2*time.Minute
		devices = append(devices, device)
	}
	return devices, rows.Err()
}
