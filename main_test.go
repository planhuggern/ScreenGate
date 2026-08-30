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

func totalFor(t *testing.T, app *application, user string) int {
	t.Helper()
	var total int
	if err := app.db.QueryRow("SELECT total_seconds FROM user_totals WHERE user = ?", user).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}

func TestHeartbeat(t *testing.T) {
	app := testApplication(t)
	req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"pc-barn1","user":"barn1","active_seconds":47}`))
	rec := httptest.NewRecorder()

	app.heartbeatHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got response
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got != (response{Action: "allow", Message: "ok"}) {
		t.Fatalf("response = %#v", got)
	}
	if total := totalFor(t, app, "barn1"); total != 47 {
		t.Fatalf("total = %d, want 47", total)
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
		req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"pc-barn1","user":"barn1","active_seconds":`+strconv.Itoa(seconds)+`}`))
		app.heartbeatHandler(httptest.NewRecorder(), req)
		req2 := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"pc-barn2","user":"barn2","active_seconds":`+strconv.Itoa(seconds)+`}`))
		app.heartbeatHandler(httptest.NewRecorder(), req2)
	}

	if total := totalFor(t, app, "barn1"); total != 120 {
		t.Fatalf("total = %d, want 120", total)
	}
}
