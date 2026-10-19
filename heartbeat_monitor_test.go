package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMonitorAccountingDuplicatesAndGaps(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "private-child-name", "private-machine-name")
	now := a.service.now()
	a.service.now = func() time.Time { return now }
	send := func(id string, locked bool, report string) {
		t.Helper()
		body := fmt.Sprintf(`{"user":%q,"device_id":%q,"heartbeat_id":%q,"locked":%t,"client_version":"2026.10.19.1"%s}`, d.User, d.DeviceID, id, locked, report)
		w := serve(a, deviceRequest(d.Token, body))
		if w.Code != 200 {
			t.Fatalf("heartbeat: %d %s", w.Code, w.Body)
		}
	}
	send("first", false, "")
	now = now.Add(30 * time.Second)
	send("second", false, `,"active_seconds":25`)
	now = now.Add(time.Second)
	send("second", false, `,"active_seconds":25`)
	page, err := a.monitorData(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 3 {
		t.Fatalf("entries=%+v", page.Entries)
	}
	if page.Entries[0].Credited != "0.000" || !strings.Contains(page.Entries[0].Note, "Duplikat") || page.Entries[0].Alert {
		t.Fatalf("duplicate=%+v", page.Entries[0])
	}
	if e := page.Entries[1]; e.Credited != "30.000" || e.Reported != "25" || !e.Alert || e.Gap != "30.000" {
		t.Fatalf("credit=%+v", e)
	}
	if page.Entries[2].Reported != "—" {
		t.Fatal("missing reported seconds shown as zero")
	}
	now = now.Add(29 * time.Second)
	send("lock", true, "")
	now = now.Add(30 * time.Second)
	send("still-locked", true, "")
	page, err = a.monitorData(0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Entries[0].Credited != "0.000" || page.Entries[0].State != "Låst" || page.Entries[1].Credited != "30.000" {
		t.Fatalf("locked credit=%+v", page.Entries[:2])
	}
	now = now.Add(64 * time.Second)
	page, err = a.monitorData(0)
	if err != nil || !page.Devices[0].Missing {
		t.Fatalf("missing=%+v err=%v", page.Devices, err)
	}
	send("after-gap", false, "")
	page, err = a.monitorData(0)
	if err != nil || page.Devices[0].Missing || !strings.Contains(page.Entries[0].Note, "Langt rapportopphold") {
		t.Fatalf("gap=%+v err=%v", page, err)
	}
	w := serve(a, httptest.NewRequest(http.MethodGet, "/monitor", nil))
	if w.Code != 200 || w.Header().Get("Refresh") != "10" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("public monitor=%d %s", w.Code, w.Body)
	}
	for _, secret := range []string{d.User, d.DeviceID, d.Token, "Authorization", "heartbeat_id"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("public monitor leaked %q", secret)
		}
	}
	views, err := a.service.repository.deviceViews()
	if err != nil || len(views) != 1 || views[0].MonitorID != page.Devices[0].ID {
		t.Fatalf("admin alias mapping=%+v err=%v", views, err)
	}
}

func TestMonitorRetentionFilteringAndPersistence(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "child", "pc")
	d2 := enrolledDevice(t, a, "another", "pc")
	for i := 0; i < 105; i++ {
		device := d
		if i%2 == 1 {
			device = d2
		}
		w := serve(a, deviceRequest(device.Token, fmt.Sprintf(`{"user":%q,"device_id":%q,"heartbeat_id":"id-%d","locked":false}`, device.User, device.DeviceID, i)))
		if w.Code != 200 {
			t.Fatalf("heartbeat=%d %s", w.Code, w.Body)
		}
	}
	page, err := a.monitorData(0)
	if err != nil || len(page.Entries) != 100 || len(page.Devices) != 2 {
		t.Fatalf("retention: %d entries, %d devices, %v", len(page.Entries), len(page.Devices), err)
	}
	filtered, err := a.monitorData(page.Devices[0].ID)
	if err != nil || len(filtered.Entries) != 50 {
		t.Fatalf("filter: %d %v", len(filtered.Entries), err)
	}
	for _, e := range filtered.Entries {
		if e.Device != page.Devices[0].ID {
			t.Fatal("filter included another device")
		}
	}
	// A fresh application over the same database retains the log and aliases.
	restarted := newApplication(a.service.repository)
	restored, err := restarted.monitorData(0)
	if err != nil || len(restored.Entries) != 100 || restored.Devices[0].ID != page.Devices[0].ID {
		t.Fatalf("restart=%+v %v", restored, err)
	}
	for _, path := range []string{"/monitor", "/monitor?device=1", "/monitor?device=999"} {
		if w := serve(a, httptest.NewRequest("GET", path, nil)); w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	if w := serve(a, httptest.NewRequest("GET", "/monitor?device=-1", nil)); w.Code != 400 {
		t.Fatal("invalid filter accepted")
	}
	if w := serve(a, httptest.NewRequest("POST", "/monitor", nil)); w.Code != 405 {
		t.Fatal("POST accepted")
	}
	if err := a.service.repository.deleteUser(d.User); err != nil {
		t.Fatal(err)
	}
	remaining, err := a.monitorData(0)
	if err != nil || len(remaining.Devices) != 1 || len(remaining.Entries) != 50 {
		t.Fatalf("user deletion retained monitoring: %+v %v", remaining, err)
	}
}

func TestMonitorRejectedRequestsAndSafeVersion(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "child", "pc")
	for _, tt := range []struct {
		body   string
		status int
	}{
		{`{`, 400},
		{`{"user":"someone-else","device_id":"pc","client_version":"private-secret"}`, 403},
		{`{"user":"child","device_id":"pc","session_state":"private-secret","client_version":"private-secret"}`, 400},
		{`{"user":"child","device_id":"pc","locked":false,"active_seconds":0,"client_version":"private-secret"}`, 200},
	} {
		if w := serve(a, deviceRequest(d.Token, tt.body)); w.Code != tt.status {
			t.Fatalf("status=%d want=%d %s", w.Code, tt.status, w.Body)
		}
	}
	page, err := a.monitorData(0)
	if err != nil || len(page.Entries) != 4 {
		t.Fatalf("entries=%+v err=%v", page.Entries, err)
	}
	if page.Entries[0].Reported != "0" || page.Entries[0].Version != "Ukjent" {
		t.Fatalf("zero/version=%+v", page.Entries[0])
	}
	if page.Entries[1].Status != 400 || page.Entries[2].Reason != "identity_mismatch" || page.Entries[3].Reason != "invalid_json" {
		t.Fatalf("errors=%+v", page.Entries)
	}
	w := serve(a, httptest.NewRequest("GET", "/monitor", nil))
	if strings.Contains(w.Body.String(), "private-secret") || strings.Contains(w.Body.String(), "someone-else") {
		t.Fatal("client-controlled strings leaked")
	}
	serve(a, deviceRequest("bad-token", `{}`))
	page, err = a.monitorData(0)
	if err != nil || len(page.Entries) != 4 {
		t.Fatal("unauthenticated request entered log")
	}
}

func TestMonitorFailureDoesNotChangeHeartbeatDecision(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "child", "pc")
	if _, err := a.service.repository.db.Exec(`DROP TABLE heartbeat_monitor`); err != nil {
		t.Fatal(err)
	}
	w := serve(a, deviceRequest(d.Token, `{"user":"child","device_id":"pc","locked":false}`))
	if w.Code != 200 {
		t.Fatalf("diagnostics failure changed authorization: %d %s", w.Code, w.Body)
	}
	if w := serve(a, httptest.NewRequest("GET", "/monitor", nil)); w.Code != 503 {
		t.Fatalf("monitor failure=%d", w.Code)
	}
}
