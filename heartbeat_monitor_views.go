package main

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

func monitorDeviceView(record monitorDeviceRecord, now time.Time, location *time.Location) monitorDevice {
	age := max(0, int(now.Sub(record.At)/time.Second))
	device := monitorDevice{
		ID: record.ID, At: record.At.In(location).Format("02.01 15:04:05"),
		Age: humanDuration(age), State: sessionLabel(record.State), Version: record.Version,
		Missing: now.Sub(record.At) > maxHeartbeatGap, Status: "Rapporterer",
	}
	if record.Status != 200 {
		device.Status = fmt.Sprintf("Rapport avvist · HTTP %d", record.Status)
	}
	if device.Missing {
		device.Status = "Rapporter uteblir"
	}
	return device
}

func monitorEntryView(record monitorRecord, location *time.Location) monitorEntry {
	entry := monitorEntry{
		Device: record.Device, At: record.At.In(location).Format("02.01.2006 15:04:05.000"),
		State: sessionLabel(record.State), Version: record.Version,
		Reported: monitorNumber(record.ReportedSeconds), Credited: monitorMilliseconds(record.CreditedMilliseconds),
		Remaining: monitorNumber(record.RemainingSeconds), Quota: monitorNumber(record.QuotaSeconds),
		Lease: monitorNumber(record.LeaseSeconds), Gap: monitorMilliseconds(record.GapMilliseconds),
		Action: record.Action, Reason: record.Reason, Status: record.Status,
	}
	if record.QuotaSeconds.Valid && record.QuotaSeconds.Int64 == 0 {
		entry.Quota, entry.Remaining = "Ubegrenset", "Ubegrenset"
	}
	entry.Note, entry.Alert = monitorNote(record)
	return entry
}

func monitorMilliseconds(value sql.NullInt64) string {
	if !value.Valid {
		return "—"
	}
	return strconv.FormatFloat(float64(value.Int64)/1000, 'f', 3, 64)
}

// Preserve the existing note priority: duplicates, accounting anomalies,
// request errors, then an optional additional warning for a long gap.
func monitorNote(record monitorRecord) (note string, alert bool) {
	if record.Duplicate {
		note = "Duplikat · ingen ny belastning"
	} else if record.Status == 200 {
		if record.State == "active" && record.CreditedMilliseconds.Int64 == 0 {
			note, alert = "Aktiv uten belastning · kan være første rapport eller langt opphold", true
		} else if record.ReportedSeconds.Valid && record.CreditedMilliseconds.Valid {
			difference := float64(record.ReportedSeconds.Int64) - float64(record.CreditedMilliseconds.Int64)/1000
			if difference > 2 || difference < -2 {
				note, alert = "Avvik mellom rapportert og belastet tid", true
			}
		}
	}
	if record.Status != 200 {
		note, alert = "Avvist / feil", true
	}
	if record.GapMilliseconds.Valid && record.GapMilliseconds.Int64 > maxHeartbeatGap.Milliseconds() {
		note += " · Langt rapportopphold"
		alert = true
	}
	return note, alert
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
