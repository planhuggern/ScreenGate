package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
	if total := dailyTotalFor(t, app, "barn1", "2026-08-31"); total != 47 {
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

func TestHeartbeatAddsToUserTotal(t *testing.T) {
	app := testApplication(t)
	for _, seconds := range []int{60, 60} {
		req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"pc-barn1","user":"barn1","active_seconds":`+strconv.Itoa(seconds)+`,"reported_at":"2026-08-31T12:00:00+02:00"}`))
		app.heartbeatHandler(httptest.NewRecorder(), req)
	}

	if total := dailyTotalFor(t, app, "barn1", "2026-08-31"); total != 120 {
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
