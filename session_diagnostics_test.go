package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"screengate/internal/sessiondiag"
)

func TestMonitorShowsReadingFailureRecoveryAndHistoryWithoutChangingUsage(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "private-child", "private-pc")
	now := a.service.now()
	a.service.now = func() time.Time { return now }
	code := uint32(5)
	diagnostics := &sessiondiag.Report{
		ObservedAt: now, Failed: true,
		LastError:   &sessiondiag.Failure{At: now, Call: "OpenInputDesktop", Reason: "call_failed", Code: &code},
		LastSuccess: &sessiondiag.Success{At: now.Add(-time.Minute), State: "active"},
	}
	h := measuredHeartbeat(d.User, d.DeviceID, "report", "batch", now.Add(-10*time.Second), now)
	h.SessionState, h.SessionDiagnostics = "locked", diagnostics
	send := func() response {
		t.Helper()
		body, _ := json.Marshal(h)
		w := serve(a, deviceRequest(d.Token, string(body)))
		var reply response
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &reply) != nil {
			t.Fatalf("diagnostics affected heartbeat: %d %s", w.Code, w.Body)
		}
		return reply
	}
	if got := send(); got.DailyTotalSeconds != 10 || got.UsageAck != "batch" {
		t.Fatal("diagnostics changed usage reconciliation")
	}
	page, err := a.monitorData(0)
	if err != nil || len(page.Entries) != 1 || !page.Entries[0].Alert || !page.Devices[0].ReadError || !strings.Contains(page.Entries[0].State, "Ukjent") {
		t.Fatalf("error not visible separately from lock: %+v %v", page, err)
	}
	for _, value := range []string{"OpenInputDesktop", "Windows-feil 5", "historikk", "Aktiv"} {
		if !strings.Contains(page.Entries[0].Diagnostics, value) {
			t.Fatalf("diagnostics missing %q: %s", value, page.Entries[0].Diagnostics)
		}
	}
	now = now.Add(time.Second)
	h.SessionDiagnostics = &sessiondiag.Report{ObservedAt: now, LastError: diagnostics.LastError, LastSuccess: &sessiondiag.Success{At: now, State: "locked"}}
	if got := send(); got.DailyTotalSeconds != 10 {
		t.Fatal("duplicate with recovered diagnostics charged usage again")
	}
	page, err = a.monitorData(0)
	if err != nil || page.Devices[0].ReadError || page.Entries[0].State != "Låst" || !strings.Contains(page.Entries[0].Diagnostics, "Siste feil") || !page.Entries[1].Alert {
		t.Fatalf("recovery erased history or still claims a current error: %+v %v", page, err)
	}
	w := serve(a, httptest.NewRequest(http.MethodGet, "/monitor", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Windows-avlesing") || !strings.Contains(w.Body.String(), "OpenInputDesktop") {
		t.Fatal("public monitor did not render diagnostics")
	}
	for _, secret := range []string{d.User, d.DeviceID, d.Token} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("public diagnostics leaked identity")
		}
	}
	// Reading history survives a new application over the same database.
	if restored, err := newApplication(a.service.repository).monitorData(0); err != nil || !strings.Contains(restored.Entries[0].Diagnostics, "OpenInputDesktop") {
		t.Fatal("diagnostics did not survive server restart")
	}
}

func TestMonitorDiscardsUnrecognizedDiagnosticTextAndKeepsOlderClients(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "child", "pc")
	now := a.service.now()
	for _, badField := range []string{"call", "reason", "missing"} {
		report := &sessiondiag.Report{ObservedAt: now, Failed: true, LastError: &sessiondiag.Failure{At: now, Call: "OpenInputDesktop", Reason: "call_failed"}}
		switch badField {
		case "call":
			report.LastError.Call = "private-path<script>"
		case "reason":
			report.LastError.Reason = "private-path<script>"
		case "missing":
			report = nil
		}
		h := heartbeat{User: d.User, DeviceID: d.DeviceID, HeartbeatID: badField, SessionState: "locked", SessionDiagnostics: report}
		body, _ := json.Marshal(h)
		w := serve(a, deviceRequest(d.Token, string(body)))
		if w.Code != 200 {
			t.Fatal("optional/absent diagnostics changed authorization")
		}
	}
	page, err := a.monitorData(0)
	if err != nil || len(page.Entries) != 3 {
		t.Fatalf("entries: %v", err)
	}
	for _, entry := range page.Entries {
		if entry.Diagnostics != "Ikke rapportert av klienten" {
			t.Fatal("unknown client text reached public view")
		}
	}
	var raw string
	if err := a.service.repository.db.QueryRow(`SELECT session_diagnostics FROM heartbeat_monitor ORDER BY id DESC LIMIT 1 OFFSET 1`).Scan(&raw); err != nil || raw != "" {
		t.Fatal("arbitrary diagnostic text was stored")
	}
}

func TestDiagnosticsMigrationPreservesExistingMonitorHistory(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "child", "pc")
	w := serve(a, deviceRequest(d.Token, `{"user":"child","device_id":"pc","heartbeat_id":"old","locked":true}`))
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	r := a.service.repository
	if err := goose.DownTo(r.db, "migrations", 7); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(r.db, "migrations"); err != nil {
		t.Fatal(err)
	}
	page, err := a.monitorData(0)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].State != "Låst" || page.Entries[0].Diagnostics != "Ikke rapportert av klienten" || len(page.Devices) != 1 {
		t.Fatalf("migration damaged old history: %+v %v", page, err)
	}
}
