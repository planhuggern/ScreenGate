package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// Never publish arbitrary client strings: versions are numeric release identifiers.
var monitorVersionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){1,4}$`)

func monitorSession(h heartbeat) string {
	if h.Locked != nil {
		if *h.Locked {
			return "locked"
		}
		return "active"
	}
	switch h.SessionState {
	case "active", "idle", "locked":
		return h.SessionState
	case "":
		return "active"
	default:
		return "unknown"
	}
}

func (a *application) observeHeartbeat(r *http.Request, h heartbeat, result heartbeatResult, at time.Time, status int, reason string) {
	// Identity comes from authentication, including for malformed/mismatched requests.
	d, authenticated := r.Context().Value(deviceContextKey{}).(deviceIdentity)
	if !authenticated {
		return
	}
	version := "Ukjent"
	if len(h.ClientVersion) <= 32 && monitorVersionPattern.MatchString(h.ClientVersion) {
		version = h.ClientVersion
	}
	state := monitorSession(h)
	if reason == "invalid_json" || reason == "identity_mismatch" {
		state, version = "unknown", "Ukjent"
	}
	if err := a.service.repository.saveMonitor(d, h, result, at, status, reason, state, version); err != nil {
		// Diagnostics must never change the authorization response.
		log.Printf("heartbeat monitoring unavailable: %v", err)
	}
}

func (r *repository) saveMonitor(d deviceIdentity, h heartbeat, result heartbeatResult, at time.Time, status int, reason, state, version string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := at.UTC().Format("2006-01-02T15:04:05.000000000Z")
	var previous string
	var gap any
	err = tx.QueryRow(`SELECT received_at FROM monitor_devices WHERE user=? AND device_id=?`, d.User, d.DeviceID).Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		last, err := time.Parse(time.RFC3339Nano, previous)
		if err != nil {
			return err
		}
		if !at.Before(last) {
			gap = at.Sub(last).Milliseconds()
		}
	}
	_, err = tx.Exec(`INSERT INTO monitor_devices(user,device_id,received_at,session_state,client_version,http_status) VALUES(?,?,?,?,?,?)
		ON CONFLICT(user,device_id) DO UPDATE SET received_at=excluded.received_at,session_state=excluded.session_state,client_version=excluded.client_version,http_status=excluded.http_status
		WHERE excluded.received_at >= monitor_devices.received_at`, d.User, d.DeviceID, stamp, state, version, status)
	if err != nil {
		return err
	}
	var reported, credit, remaining, quota, lease any
	action := ""
	if h.ReportedActivityPresent && reason != "invalid_json" && reason != "identity_mismatch" {
		reported = h.ActiveSeconds
	}
	if status == http.StatusOK {
		credit, remaining, quota, lease = result.CreditedMilliseconds, result.RemainingSeconds, result.QuotaSeconds, result.LeaseSeconds
		action = result.Action
	}
	_, err = tx.Exec(`INSERT INTO heartbeat_monitor(device,received_at,gap_milliseconds,session_state,client_version,reported_seconds,credited_milliseconds,remaining_seconds,quota_seconds,lease_seconds,action,reason,duplicate,http_status)
		VALUES((SELECT id FROM monitor_devices WHERE user=? AND device_id=?),?,?,?,?,?,?,?,?,?,?,?,?,?)`, d.User, d.DeviceID, stamp, gap, state, version, reported, credit, remaining, quota, lease, action, reason, result.Duplicate, status)
	if err != nil {
		return err
	}
	// Keep exactly the latest 100 arrivals, including duplicates and rejected payloads.
	_, err = tx.Exec(`DELETE FROM heartbeat_monitor WHERE id NOT IN (SELECT id FROM heartbeat_monitor ORDER BY id DESC LIMIT 100)`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

type monitorDevice struct {
	ID                              int64
	At, Age, State, Version, Status string
	Missing                         bool
}

type monitorEntry struct {
	Gap                                                                                   string
	Device                                                                                int64
	At, State, Version, Reported, Credited, Remaining, Quota, Lease, Action, Reason, Note string
	Status                                                                                int
	Alert                                                                                 bool
}

type monitorPage struct {
	AdminPath, Updated, Timezone string
	Selected                     int64
	Devices                      []monitorDevice
	Entries                      []monitorEntry
}

func monitorNumber(n sql.NullInt64) string {
	if !n.Valid {
		return "—"
	}
	return strconv.FormatInt(n.Int64, 10)
}

func sessionLabel(state string) string {
	switch state {
	case "active":
		return "Aktiv"
	case "idle":
		return "Inaktiv"
	case "locked":
		return "Låst"
	default:
		return "Ukjent"
	}
}

func (a *application) monitorData(selected int64) (monitorPage, error) {
	now := a.service.now()
	location := a.service.location
	page := monitorPage{AdminPath: a.adminPath, Selected: selected, Updated: now.In(location).Format("02.01.2006 15:04:05"), Timezone: location.String()}
	rows, err := a.service.repository.db.Query(`SELECT id,received_at,session_state,client_version,http_status FROM monitor_devices ORDER BY id`)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var d monitorDevice
		var raw, state string
		var status int
		if err := rows.Scan(&d.ID, &raw, &state, &d.Version, &status); err != nil {
			rows.Close()
			return page, err
		}
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			rows.Close()
			return page, err
		}
		age := max(0, int(now.Sub(at)/time.Second))
		d.At, d.Age, d.State = at.In(location).Format("02.01 15:04:05"), humanDuration(age), sessionLabel(state)
		d.Missing = now.Sub(at) > maxHeartbeatGap
		d.Status = "Rapporterer"
		if status != 200 {
			d.Status = fmt.Sprintf("Rapport avvist · HTTP %d", status)
		}
		if d.Missing {
			d.Status = "Rapporter uteblir"
		}
		page.Devices = append(page.Devices, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	// Filtering applies to the global last 100, not a separate unbounded history.
	rows, err = a.service.repository.db.Query(`SELECT device,received_at,gap_milliseconds,session_state,client_version,reported_seconds,credited_milliseconds,remaining_seconds,quota_seconds,lease_seconds,action,reason,duplicate,http_status FROM heartbeat_monitor WHERE (?=0 OR device=?) ORDER BY id DESC LIMIT 100`, selected, selected)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var e monitorEntry
		var raw, state string
		var reported, credit, remaining, quota, lease, gap sql.NullInt64
		var duplicate bool
		if err := rows.Scan(&e.Device, &raw, &gap, &state, &e.Version, &reported, &credit, &remaining, &quota, &lease, &e.Action, &e.Reason, &duplicate, &e.Status); err != nil {
			return page, err
		}
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return page, err
		}
		e.At, e.State = at.In(location).Format("02.01.2006 15:04:05.000"), sessionLabel(state)
		e.Reported, e.Remaining, e.Quota, e.Lease = monitorNumber(reported), monitorNumber(remaining), monitorNumber(quota), monitorNumber(lease)
		e.Credited = "—"
		e.Gap = "—"
		if gap.Valid {
			e.Gap = strconv.FormatFloat(float64(gap.Int64)/1000, 'f', 3, 64)
		}
		if credit.Valid {
			e.Credited = strconv.FormatFloat(float64(credit.Int64)/1000, 'f', 3, 64)
		}
		if quota.Valid && quota.Int64 == 0 {
			e.Quota, e.Remaining = "Ubegrenset", "Ubegrenset"
		}
		if duplicate {
			e.Note = "Duplikat · ingen ny belastning"
		} else if e.Status == 200 {
			if state == "active" && credit.Int64 == 0 {
				e.Note, e.Alert = "Aktiv uten belastning · kan være første rapport eller langt opphold", true
			} else if reported.Valid && credit.Valid && (float64(reported.Int64)-float64(credit.Int64)/1000 > 2 || float64(credit.Int64)/1000-float64(reported.Int64) > 2) {
				e.Note, e.Alert = "Avvik mellom rapportert og belastet tid", true
			}
		}
		if e.Status != 200 {
			e.Note, e.Alert = "Avvist / feil", true
		}
		if gap.Valid && gap.Int64 > maxHeartbeatGap.Milliseconds() {
			e.Note += " · Langt rapportopphold"
			e.Alert = true
		}
		page.Entries = append(page.Entries, e)
	}
	return page, rows.Err()
}

var monitorTemplate = template.Must(template.New("monitor.html").ParseFS(templateFiles, "templates/monitor.html", "templates/styles.html"))

func (a *application) monitorHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	var selected int64
	if raw := r.URL.Query().Get("device"); raw != "" {
		var err error
		selected, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || selected < 0 {
			http.Error(w, "invalid device filter", http.StatusBadRequest)
			return
		}
	}
	page, err := a.monitorData(selected)
	if err != nil {
		log.Printf("monitor: %v", err)
		http.Error(w, "monitor unavailable", http.StatusServiceUnavailable)
		return
	}
	var body bytes.Buffer
	if err := monitorTemplate.Execute(&body, page); err != nil {
		log.Printf("monitor template: %v", err)
		http.Error(w, "monitor unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Refresh", "10")
	_, _ = w.Write(body.Bytes())
}
