package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type auditEvent struct{ At, User, Description string }

func (a *application) recentAudit() ([]auditEvent, error) {
	rows, err := a.service.repository.db.Query("SELECT occurred_at,user,action,detail FROM audit_events ORDER BY occurred_at DESC,id DESC LIMIT 30")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []auditEvent
	for rows.Next() {
		var event auditEvent
		var at int64
		var action, detail string
		if err := rows.Scan(&at, &event.User, &action, &detail); err != nil {
			return nil, err
		}
		event.At = time.Unix(at, 0).In(a.service.location).Format("02.01.2006 15:04")
		switch action {
		case "quota":
			seconds, _ := strconv.Atoi(detail)
			event.Description = "Dagsgrense satt til " + humanDuration(seconds)
			if seconds == 0 {
				event.Description = "Daglig tidsgrense fjernet"
			}
		case "schedule":
			event.Description = "Ukeplan oppdatert"
		case "pause":
			event.Description = "Skjermtilgang satt på pause"
			if detail == "false" {
				event.Description = "Skjermtilgang åpnet igjen"
			}
		case "bonus":
			if strings.HasPrefix(detail, "reset") {
				event.Description = "Dagens ekstratid fjernet"
			} else {
				fields := strings.Fields(detail)
				minutes := 0
				if len(fields) > 0 {
					minutes, _ = strconv.Atoi(fields[len(fields)-1])
				}
				event.Description = "Lagt til " + humanDuration(minutes*60) + " ekstra i dag"
			}
		case "pairing":
			event.Description = "Ny koblingskode laget"
		case "device_enrolled":
			event.Description = "Enhet koblet til: " + detail
		case "device_revoked":
			event.Description = "Enhet koblet fra"
		case "backup":
			event.Description = "Sikkerhetskopi lastet ned"
		default:
			event.Description = action
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (a *application) backupHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	// SQLite creates a consistent snapshot, including writes still in its WAL.
	// Copying the live .db file directly can silently omit recent changes.
	file, err := os.CreateTemp("", "screengate-backup-*.db")
	if err != nil {
		http.Error(w, "backup unavailable", http.StatusInternalServerError)
		return
	}
	path := file.Name()
	file.Close()
	defer os.Remove(path)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := a.service.repository.db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		http.Error(w, "backup unavailable", http.StatusInternalServerError)
		return
	}
	a.audit("", "backup", "")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="screengate-%s.db"`, a.service.today()))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}
