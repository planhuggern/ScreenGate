package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeartbeatProtocolRejectsInvalidAuthorization(t *testing.T) {
	tests := []struct {
		name              string
		status            int
		contentType, body string
	}{
		{"HTTP error with allow body", 500, "application/json", `{"action":"allow","lease_seconds":90}`},
		{"unauthorized", 401, "application/json", `{"action":"allow","lease_seconds":90}`},
		{"login redirect", 302, "application/json", `{"action":"allow","lease_seconds":90}`},
		{"wrong content type", 200, "text/html", `{"action":"allow","lease_seconds":90}`},
		{"malformed", 200, "application/json", `{`},
		{"empty", 200, "application/json", `{}`},
		{"trailing document", 200, "application/json", `{"action":"allow","lease_seconds":90}{}`},
		{"unknown action", 200, "application/json", `{"action":"unlock","lease_seconds":90}`},
		{"missing lease", 200, "application/json", `{"action":"allow"}`},
		{"empty remaining", 200, "application/json", `{"action":"allow","lease_seconds":90,"quota_seconds":60}`},
		{"negative remaining", 200, "application/json", `{"action":"allow","lease_seconds":90,"remaining_seconds":-1}`},
		{"negative quota", 200, "application/json", `{"action":"allow","lease_seconds":90,"quota_seconds":-1}`},
		{"negative version", 200, "application/json", `{"action":"allow","lease_seconds":90,"policy_version":-1}`},
		{"oversized", 200, "application/json", `{"action":"allow","lease_seconds":90,"message":"` + strings.Repeat("x", 20*1024) + `"}`},
		{"oversized whitespace", 200, "application/json", `{"action":"allow","lease_seconds":90}` + strings.Repeat(" ", 20*1024)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			_, err := postHeartbeat(context.Background(), newHTTPClient(), server.URL, "test-token", heartbeat{User: "child", DeviceID: "pc"})
			if err == nil {
				t.Fatal("invalid authorization accepted")
			}
		})
	}
}

func TestHeartbeatProtocolAuthenticatesAndPreservesReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("request lacks authentication")
		}
		var report heartbeat
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
		}
		if report.HeartbeatID != "idempotent-report" || report.ActiveSeconds != 25 || report.ActivityDate != "2026-09-18" {
			t.Errorf("report=%+v", report)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"action":"allow","lease_seconds":90,"remaining_seconds":120,"quota_seconds":600,"policy_version":7,"future_field":true}`))
	}))
	defer server.Close()
	result, err := postHeartbeat(context.Background(), newHTTPClient(), server.URL, "secret", heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "idempotent-report", ActiveSeconds: 25, ActivityDate: "2026-09-18"})
	if err != nil || result.Action != "allow" || result.PolicyVersion != 7 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestHTTPClientDoesNotForwardCredentialsThroughRedirect(t *testing.T) {
	contacted := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := postHeartbeat(context.Background(), newHTTPClient(), server.URL, "secret", heartbeat{})
	if err == nil || contacted {
		t.Fatal("redirect was followed")
	}
}

func TestValidateEndpoint(t *testing.T) {
	for _, value := range []string{"http://localhost:8081/heartbeat", "https://example.test/heartbeat", "http://[::1]:8081/heartbeat"} {
		if _, err := validateEndpoint(value); err != nil {
			t.Errorf("valid URL %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "file:///heartbeat", "https://user:password@example.test/heartbeat", "https://example.test/heartbeat?token=secret", "https://example.test/heartbeat#x", "https://example.test/admin", "//example.test/heartbeat"} {
		if _, err := validateEndpoint(value); err == nil {
			t.Errorf("invalid URL accepted: %q", value)
		}
	}
}
