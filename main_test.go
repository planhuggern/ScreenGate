package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestHeartbeat(t *testing.T) {
	app := newApplication()
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
	if app.totals["barn1"] != 47 {
		t.Fatalf("total = %d, want 47", app.totals["barn1"])
	}
}

func TestHeartbeatEmptyRequiredFieldsStillAllows(t *testing.T) {
	app := newApplication()
	req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"","user":"","active_seconds":0}`))
	rec := httptest.NewRecorder()

	app.heartbeatHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHeartbeatRejectsOtherMethods(t *testing.T) {
	app := newApplication()
	req := httptest.NewRequest(http.MethodGet, "/heartbeat", nil)
	rec := httptest.NewRecorder()

	app.heartbeatHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHeartbeatAddsToUserTotal(t *testing.T) {
	app := newApplication()
	for _, seconds := range []int{60, 60} {
		req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"pc-barn1","user":"barn1","active_seconds":`+strconv.Itoa(seconds)+`}`))
		app.heartbeatHandler(httptest.NewRecorder(), req)
	}

	if app.totals["barn1"] != 120 {
		t.Fatalf("total = %d, want 120", app.totals["barn1"])
	}
}
