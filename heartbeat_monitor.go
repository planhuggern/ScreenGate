package main

import (
	"bytes"
	"database/sql"
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
	record := monitorRecordFromHeartbeat(h, result, at, status, reason)
	if err := a.service.repository.saveMonitor(d, record); err != nil {
		// Diagnostics must never change the authorization response.
		log.Printf("heartbeat monitoring unavailable: %v", err)
	}
}

// Sanitize client-provided fields before storage. Failed requests have no quota
// decision; an omitted activity value must remain different from zero.
func monitorRecordFromHeartbeat(h heartbeat, result heartbeatResult, at time.Time, status int, reason string) monitorRecord {
	version := "Ukjent"
	if len(h.ClientVersion) <= 32 && monitorVersionPattern.MatchString(h.ClientVersion) {
		version = h.ClientVersion
	}
	state := monitorSession(h)
	if reason == "invalid_json" || reason == "identity_mismatch" {
		state, version = "unknown", "Ukjent"
	}
	record := monitorRecord{At: at, State: state, Version: version, Status: status, Reason: reason, Duplicate: result.Duplicate}
	if h.SessionDiagnostics != nil && h.SessionDiagnostics.Valid() && reason != "invalid_json" && reason != "identity_mismatch" {
		record.Diagnostics = h.SessionDiagnostics
	}
	if h.ReportedActivityPresent && reason != "invalid_json" && reason != "identity_mismatch" {
		record.ReportedSeconds = sql.NullInt64{Int64: int64(h.ActiveSeconds), Valid: true}
	}
	if status == http.StatusOK {
		record.CreditedMilliseconds = sql.NullInt64{Int64: result.CreditedMilliseconds, Valid: true}
		record.RemainingSeconds = sql.NullInt64{Int64: int64(result.RemainingSeconds), Valid: true}
		record.QuotaSeconds = sql.NullInt64{Int64: int64(result.QuotaSeconds), Valid: true}
		record.LeaseSeconds = sql.NullInt64{Int64: int64(result.LeaseSeconds), Valid: true}
		record.Action = result.Action
	}
	return record
}

// Loading and formatting are separate: the repository returns numbers and
// timestamps, and the views turn those into dashboard text.
func (a *application) monitorData(selected int64) (monitorPage, error) {
	now := a.service.now()
	location := a.service.location
	page := monitorPage{AdminPath: a.adminPath, Selected: selected, Updated: now.In(location).Format("02.01.2006 15:04:05"), Timezone: location.String()}
	devices, err := a.service.repository.monitorDevices()
	for _, device := range devices {
		page.Devices = append(page.Devices, monitorDeviceView(device, now, location))
	}
	if err != nil {
		return page, err
	}
	entries, err := a.service.repository.monitorEntries(selected)
	for _, entry := range entries {
		page.Entries = append(page.Entries, monitorEntryView(entry, location))
	}
	return page, err
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
