package main

import (
	"database/sql"
	"encoding/json"
	"time"

	"screengate/internal/sessiondiag"
)

// Storage records contain raw values. Public aliases, labels and warning text
// are prepared by heartbeat_monitor_views.go, not by database queries.
type monitorDeviceRecord struct {
	Diagnostics *sessiondiag.Report
	ID          int64
	At          time.Time
	State       string
	Version     string
	Status      int
}

type monitorRecord struct {
	Diagnostics          *sessiondiag.Report
	Device               int64
	At                   time.Time
	State                string
	Version              string
	ReportedSeconds      sql.NullInt64
	CreditedMilliseconds sql.NullInt64
	RemainingSeconds     sql.NullInt64
	QuotaSeconds         sql.NullInt64
	LeaseSeconds         sql.NullInt64
	GapMilliseconds      sql.NullInt64
	Action               string
	Reason               string
	Duplicate            bool
	Status               int
}

func (r *repository) saveMonitor(d deviceIdentity, record monitorRecord) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := record.At.UTC().Format("2006-01-02T15:04:05.000000000Z")
	var previous string
	var gap sql.NullInt64
	err = tx.QueryRow(`SELECT received_at FROM monitor_devices WHERE user=? AND device_id=?`, d.User, d.DeviceID).Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		last, err := time.Parse(time.RFC3339Nano, previous)
		if err != nil {
			return err
		}
		if !record.At.Before(last) {
			gap = sql.NullInt64{Int64: record.At.Sub(last).Milliseconds(), Valid: true}
		}
	}
	_, err = tx.Exec(`INSERT INTO monitor_devices(user,device_id,received_at,session_state,client_version,http_status,session_diagnostics) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(user,device_id) DO UPDATE SET received_at=excluded.received_at,session_state=excluded.session_state,client_version=excluded.client_version,http_status=excluded.http_status,session_diagnostics=excluded.session_diagnostics
		WHERE excluded.received_at >= monitor_devices.received_at`, d.User, d.DeviceID, stamp, record.State, record.Version, record.Status, encodeSessionDiagnostics(record.Diagnostics))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO heartbeat_monitor(device,received_at,gap_milliseconds,session_state,client_version,reported_seconds,credited_milliseconds,remaining_seconds,quota_seconds,lease_seconds,action,reason,duplicate,http_status,session_diagnostics)
		VALUES((SELECT id FROM monitor_devices WHERE user=? AND device_id=?),?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, d.User, d.DeviceID, stamp, gap, record.State, record.Version, record.ReportedSeconds, record.CreditedMilliseconds, record.RemainingSeconds, record.QuotaSeconds, record.LeaseSeconds, record.Action, record.Reason, record.Duplicate, record.Status, encodeSessionDiagnostics(record.Diagnostics))
	if err != nil {
		return err
	}
	// Keep the global last 100, including duplicate and rejected requests.
	_, err = tx.Exec(`DELETE FROM heartbeat_monitor WHERE id NOT IN (SELECT id FROM heartbeat_monitor ORDER BY id DESC LIMIT 100)`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *repository) monitorDevices() ([]monitorDeviceRecord, error) {
	rows, err := r.db.Query(`SELECT id,received_at,session_state,client_version,http_status,session_diagnostics FROM monitor_devices ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var devices []monitorDeviceRecord
	for rows.Next() {
		var device monitorDeviceRecord
		var raw, diagnostics string
		if err := rows.Scan(&device.ID, &raw, &device.State, &device.Version, &device.Status, &diagnostics); err != nil {
			return devices, err
		}
		device.At, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return devices, err
		}
		device.Diagnostics = decodeSessionDiagnostics(diagnostics)
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

func (r *repository) monitorEntries(selected int64) ([]monitorRecord, error) {
	// Filtering applies to the stored global last 100, not to a separate history.
	rows, err := r.db.Query(`SELECT device,received_at,gap_milliseconds,session_state,client_version,reported_seconds,credited_milliseconds,remaining_seconds,quota_seconds,lease_seconds,action,reason,duplicate,http_status,session_diagnostics FROM heartbeat_monitor WHERE (?=0 OR device=?) ORDER BY id DESC LIMIT 100`, selected, selected)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []monitorRecord
	for rows.Next() {
		var entry monitorRecord
		var raw, diagnostics string
		if err := rows.Scan(&entry.Device, &raw, &entry.GapMilliseconds, &entry.State, &entry.Version, &entry.ReportedSeconds, &entry.CreditedMilliseconds, &entry.RemainingSeconds, &entry.QuotaSeconds, &entry.LeaseSeconds, &entry.Action, &entry.Reason, &entry.Duplicate, &entry.Status, &diagnostics); err != nil {
			return entries, err
		}
		entry.At, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return entries, err
		}
		entry.Diagnostics = decodeSessionDiagnostics(diagnostics)
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func encodeSessionDiagnostics(report *sessiondiag.Report) string {
	if report == nil || !report.Valid() {
		return ""
	}
	data, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	return string(data)
}

func decodeSessionDiagnostics(raw string) *sessiondiag.Report {
	var report sessiondiag.Report
	if json.Unmarshal([]byte(raw), &report) != nil || !report.Valid() {
		return nil
	}
	return &report
}
