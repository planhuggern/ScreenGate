package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testApplication(t *testing.T) *application {
	t.Helper()
	db, err := openDatabase(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return newApplication(db)
}

func dailyTotalFor(t *testing.T, app *application, user, date string) int {
	t.Helper()
	var total int
	if err := app.db.QueryRow("SELECT total_seconds FROM daily_totals WHERE user = ? AND date = ?", user, date).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}

func TestHeartbeat(t *testing.T) {
	app := testApplication(t)
	req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"pc-barn1","user":"barn1","active_seconds":47,"reported_at":"2026-08-31T12:00:00+02:00"}`))
	rec := httptest.NewRecorder()

	app.heartbeatHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got response
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got != (response{Action: "allow", Message: "ok", DailyTotalSeconds: 47}) {
		t.Fatalf("response = %#v", got)
	}
	if total := dailyTotalFor(t, app, "barn1", today()); total != 47 {
		t.Fatalf("daily total = %d, want 47", total)
	}
}

func TestHeartbeatEmptyRequiredFieldsStillAllows(t *testing.T) {
	app := testApplication(t)
	req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"","user":"","active_seconds":0}`))
	rec := httptest.NewRecorder()

	app.heartbeatHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHeartbeatRejectsOtherMethods(t *testing.T) {
	app := testApplication(t)
	req := httptest.NewRequest(http.MethodGet, "/heartbeat", nil)
	rec := httptest.NewRecorder()

	app.heartbeatHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestEventAcceptsValidEvent(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/event", strings.NewReader(`{"type":"focus_changed","device_id":"pc-barn1","user":"barn1","previous_app":"roblox.exe","active_seconds":332,"timestamp":"2026-08-31T22:15:03+02:00"}`))
	rec := httptest.NewRecorder()

	eventHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestEventRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: `{`},
		{name: "empty device", body: `{"type":"focus_changed","device_id":"","user":"barn1","previous_app":"roblox.exe","active_seconds":1,"timestamp":"2026-08-31T22:15:03+02:00"}`},
		{name: "empty user", body: `{"type":"focus_changed","device_id":"pc-barn1","user":"","previous_app":"roblox.exe","active_seconds":1,"timestamp":"2026-08-31T22:15:03+02:00"}`},
		{name: "negative seconds", body: `{"type":"focus_changed","device_id":"pc-barn1","user":"barn1","previous_app":"roblox.exe","active_seconds":-1,"timestamp":"2026-08-31T22:15:03+02:00"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			eventHandler(rec, httptest.NewRequest(http.MethodPost, "/event", strings.NewReader(test.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestHeartbeatAddsToUserTotal(t *testing.T) {
	app := testApplication(t)
	for _, seconds := range []int{60, 60} {
		req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"pc-barn1","user":"barn1","active_seconds":`+strconv.Itoa(seconds)+`,"reported_at":"2026-08-31T12:00:00+02:00"}`))
		app.heartbeatHandler(httptest.NewRecorder(), req)
	}

	if total := dailyTotalFor(t, app, "barn1", today()); total != 120 {
		t.Fatalf("daily total = %d, want 120", total)
	}
	var heartbeatCount int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM heartbeats WHERE user = ?", "barn1").Scan(&heartbeatCount); err != nil {
		t.Fatal(err)
	}
	if heartbeatCount != 2 {
		t.Fatalf("heartbeat rows = %d, want 2", heartbeatCount)
	}
}

func TestDashboardShowsTodaysActivity(t *testing.T) {
	app := testApplication(t)
	if _, err := app.addHeartbeat(heartbeat{DeviceID: "pc-barn1", User: "barn1", ActiveSeconds: 60, ReportedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()

	app.dashboardHandler(rec, httptest.NewRequest(http.MethodGet, app.adminPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "barn1") || !strings.Contains(rec.Body.String(), "1m0s") {
		t.Fatalf("dashboard did not contain activity: %s", rec.Body.String())
	}
}

func TestOverviewShowsActivityWithoutAdminControls(t *testing.T) {
	app := testApplication(t)
	if _, err := app.addHeartbeat(heartbeat{DeviceID: "pc-barn1", User: "barn1", ActiveSeconds: 60, ReportedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()

	app.overviewHandler(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	page := rec.Body.String()
	if !strings.Contains(page, "barn1") || strings.Contains(page, "Last ned installasjon") || strings.Contains(page, "name=\"hours\"") {
		t.Fatalf("unexpected overview page: %s", page)
	}
}

func TestHeartbeatLocksWhenDailyQuotaIsReached(t *testing.T) {
	app := testApplication(t)
	if err := app.setUserQuota("barn1", 1); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"pc-barn1","user":"barn1","active_seconds":60,"reported_at":"2026-08-31T12:00:00+02:00"}`))
	rec := httptest.NewRecorder()

	app.heartbeatHandler(rec, req)

	var got response
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Action != "lock" {
		t.Fatalf("action = %q, want lock", got.Action)
	}
	if got.PolicyVersion != 1 {
		t.Fatalf("policy version = %d, want 1", got.PolicyVersion)
	}
}

func TestUserQuotaHandler(t *testing.T) {
	app := testApplication(t)
	req := httptest.NewRequest(http.MethodPost, "/user-quota", strings.NewReader("user=barn1&hours=1&minutes=30"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	app.userQuotaHandler(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	quota, err := app.userQuota("barn1")
	if err != nil {
		t.Fatal(err)
	}
	if quota != 5400 {
		t.Fatalf("quota = %d, want 5400", quota)
	}
}

func TestPolicyVersionIncrementsWhenQuotaChanges(t *testing.T) {
	app := testApplication(t)
	if err := app.setUserQuota("barn1", 3600); err != nil {
		t.Fatal(err)
	}
	version, err := app.userPolicyVersion("barn1")
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("version = %d, want 1", version)
	}
	if err := app.setUserQuota("barn1", 3600); err != nil {
		t.Fatal(err)
	}
	version, err = app.userPolicyVersion("barn1")
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("version = %d, want 1 after unchanged quota", version)
	}
	if err := app.setUserQuota("barn1", 7200); err != nil {
		t.Fatal(err)
	}
	version, err = app.userPolicyVersion("barn1")
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("version = %d, want 2", version)
	}
}

func TestScreenTimeDecision(t *testing.T) {
	tests := []struct {
		name      string
		daily     int
		quota     int
		action    string
		remaining int
	}{
		{name: "time remaining", daily: 3120, quota: 3600, action: "allow", remaining: 480},
		{name: "exactly at limit", daily: 3600, quota: 3600, action: "lock", remaining: 0},
		{name: "over limit", daily: 3700, quota: 3600, action: "lock", remaining: 0},
		{name: "unlimited", daily: 999999, quota: 0, action: "allow", remaining: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action, remaining := screenTimeDecision(test.daily, test.quota)
			if action != test.action || remaining != test.remaining {
				t.Fatalf("decision = %q, %d; want %q, %d", action, remaining, test.action, test.remaining)
			}
			if remaining < 0 {
				t.Fatalf("remaining = %d, must not be negative", remaining)
			}
		})
	}
}

func TestHeartbeatReturnsRemainingSeconds(t *testing.T) {
	app := testApplication(t)
	if err := app.setUserQuota("barn1", 3600); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"pc-barn1","user":"barn1","active_seconds":3120,"reported_at":"2026-09-04T12:00:00+02:00"}`))
	rec := httptest.NewRecorder()

	app.heartbeatHandler(rec, req)

	var got response
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Action != "allow" || got.DailyTotalSeconds != 3120 || got.RemainingSeconds != 480 {
		t.Fatalf("response = %#v", got)
	}
}

func TestDownloadClientRejectsOtherMethods(t *testing.T) {
	rec := httptest.NewRecorder()

	downloadClientHandler(rec, httptest.NewRequest(http.MethodPost, "/downloads/screengate-client.exe", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestDownloadInstallerRejectsOtherMethods(t *testing.T) {
	rec := httptest.NewRecorder()

	downloadInstallerHandler(rec, httptest.NewRequest(http.MethodPost, "/downloads/install.ps1", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
