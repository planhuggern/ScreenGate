package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestInstallerUsesDownloadOrigin(t *testing.T) {
	for _, tc := range []struct{ download, trusted, want string }{
		{"http://10.0.0.20:8081/downloads/install.ps1", "", "http://10.0.0.20:8081/heartbeat"},
		{"https://screen.example/admin/downloads/install.ps1", "", "https://screen.example/heartbeat"},
		{"http://screen.example/downloads/install.ps1", "https://screen.example", "https://screen.example/heartbeat"},
		{"http://10.0.0.20:8081/downloads/install.ps1", "https://other.example", "http://10.0.0.20:8081/heartbeat"},
		{"http://[::1]:8081/downloads/install.ps1", "", "http://[::1]:8081/heartbeat"},
	} {
		t.Run(tc.download+tc.trusted, func(t *testing.T) {
			t.Setenv("SCREENGATE_TRUSTED_ORIGINS", tc.trusted)
			r := httptest.NewRequest("GET", tc.download, nil)
			r.Header.Set("X-Forwarded-Host", "attacker.example")
			w := httptest.NewRecorder()
			downloadInstallerHandler(w, r)
			body := w.Body.String()
			if w.Code != 200 || !strings.Contains(body, "FromBase64String('"+base64.StdEncoding.EncodeToString([]byte(tc.want))+"')") || strings.Contains(body, "SCREENGATE_SERVER_DEFAULT") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("installer did not embed the expected origin")
			}
		})
	}
}

func installerPairingCode(t *testing.T, script string) string {
	t.Helper()
	match := regexp.MustCompile(`\$EnrollmentCode = .*?FromBase64String\('([^']+)'\)`).FindStringSubmatch(script)
	if len(match) != 2 {
		t.Fatal("installer has no embedded pairing code")
	}
	code, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatal(err)
	}
	return string(code)
}

func TestPersonalizedInstallerEnrollsSelectedUserOnce(t *testing.T) {
	a := securedApplication(t)
	w := serve(a, adminRequest(a, http.MethodPost, a.adminPath+"/devices/pair", url.Values{"user": {"Espen"}}))
	if w.Code != http.StatusOK || w.Header().Get("Content-Disposition") != "attachment; filename=install.ps1" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("download: %d %v", w.Code, w.Header())
	}
	code := installerPairingCode(t, w.Body.String())
	if strings.Contains(w.Body.String(), "SCREENGATE_ENROLLMENT_DEFAULT") {
		t.Fatal("unexpanded placeholder")
	}
	device, err := a.service.repository.enrollDevice(code, "test-pc", a.service.now())
	if err != nil || device.User != "Espen" {
		t.Fatalf("device=%+v err=%v", device, err)
	}
	if _, err := a.service.repository.enrollDevice(code, "another-pc", a.service.now()); err != errInvalidPairing {
		t.Fatalf("code reused: %v", err)
	}
	var leaked int
	if err := a.service.repository.db.QueryRow("SELECT COUNT(*) FROM audit_events WHERE detail LIKE ?", "%"+code+"%").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("code in audit: count=%d err=%v", leaked, err)
	}
}

func TestDownloadedInstallerCodeExpiresAndNewDownloadReplacesIt(t *testing.T) {
	a := securedApplication(t)
	download := func() string {
		w := serve(a, adminRequest(a, http.MethodPost, a.adminPath+"/devices/pair", url.Values{"user": {"Espen"}}))
		if w.Code != http.StatusOK {
			t.Fatalf("download status=%d", w.Code)
		}
		return installerPairingCode(t, w.Body.String())
	}
	first, second := download(), download()
	if first == second {
		t.Fatal("downloads reused the code")
	}
	if _, err := a.service.repository.enrollDevice(first, "pc", a.service.now()); err != errInvalidPairing {
		t.Fatal("superseded code accepted")
	}
	if _, err := a.service.repository.enrollDevice(second, "pc", a.service.now().Add(15*time.Minute)); err != errInvalidPairing {
		t.Fatal("expired code accepted")
	}
}

func TestGenericInstallerHasNoEmbeddedPairingCode(t *testing.T) {
	w := httptest.NewRecorder()
	downloadInstallerHandler(w, httptest.NewRequest("GET", "http://localhost/downloads/install.ps1?code=untrusted", nil))
	if !strings.Contains(w.Body.String(), "$EnrollmentCode = '',") || strings.Contains(w.Body.String(), "SCREENGATE_ENROLLMENT_DEFAULT") || strings.Contains(w.Body.String(), "untrusted") {
		t.Fatal("generic installer includes an unexpected code")
	}
	if !strings.Contains(w.Body.String(), "$EnrollmentCode = Read-Host") {
		t.Fatal("interactive code entry missing")
	}
}
