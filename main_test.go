package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeartbeat(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"pc-barn1","user":"barn1","active_seconds":47}`))
	rec := httptest.NewRecorder()

	heartbeatHandler(rec, req)

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
}

func TestHeartbeatEmptyRequiredFieldsStillAllows(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"","user":"","active_seconds":0}`))
	rec := httptest.NewRecorder()

	heartbeatHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHeartbeatRejectsOtherMethods(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/heartbeat", nil)
	rec := httptest.NewRecorder()

	heartbeatHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
