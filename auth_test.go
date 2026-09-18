package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func securedApplication(t *testing.T) *application {
	t.Helper()
	a := testApplication(t)
	a.adminPassword = "test-password-at-least-twelve"
	return a
}

func adminRequest(a *application, method, path string, form url.Values) *http.Request {
	var body string
	if form != nil {
		form.Set("csrf_token", a.csrfToken)
		body = form.Encode()
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.SetBasicAuth(a.adminUser, a.adminPassword)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return r
}

func serve(a *application, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

func enrolledDevice(t *testing.T, a *application, user, device string) enrollmentResponse {
	t.Helper()
	code, err := a.service.repository.createPairingCode(user, a.service.now())
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := a.service.repository.enrollDevice(code, device, a.service.now())
	if err != nil {
		t.Fatal(err)
	}
	return credentials
}

func deviceRequest(token, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestRoutesProtectEveryAdminEndpoint(t *testing.T) {
	a := securedApplication(t)
	for _, path := range []string{"", "/user-quota", "/user-policy", "/user-bonus", "/user-pause", "/devices/pair", "/devices/revoke", "/export.csv", "/downloads/install.ps1", "/downloads/screengate-client.exe"} {
		t.Run(path, func(t *testing.T) {
			w := serve(a, httptest.NewRequest(http.MethodGet, a.adminPath+path, nil))
			if w.Code != http.StatusUnauthorized && w.Code != http.StatusTooManyRequests {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("admin response may be cached")
			}
		})
	}
	a.loginLimiter.reset("192.0.2.1")
	w := serve(a, adminRequest(a, http.MethodGet, a.adminPath, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("valid admin status=%d body=%s", w.Code, w.Body)
	}
}

func TestAdminRejectsMissingCSRFAndCrossOrigin(t *testing.T) {
	a := securedApplication(t)
	for _, origin := range []string{"", "https://attacker.example"} {
		r := httptest.NewRequest(http.MethodPost, a.adminPath+"/user-quota", strings.NewReader("user=child&hours=1&minutes=0"))
		r.SetBasicAuth(a.adminUser, a.adminPassword)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := serve(a, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("origin %q status=%d", origin, w.Code)
		}
	}
	w := serve(a, adminRequest(a, http.MethodPost, a.adminPath+"/user-quota", url.Values{"user": {"child"}, "hours": {"1"}, "minutes": {"30"}}))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("valid form status=%d body=%s", w.Code, w.Body)
	}
	quota, err := a.service.repository.userQuota("child")
	if err != nil || quota != 5400 {
		t.Fatalf("quota=%d err=%v", quota, err)
	}
}

func TestConfiguredTrustedOriginAllowsProxyOrigin(t *testing.T) {
	t.Setenv("SCREENGATE_TRUSTED_ORIGINS", "https://screen.example.no")
	a := securedApplication(t)
	r := adminRequest(a, http.MethodPost, a.adminPath+"/user-quota", url.Values{"user": {"child"}, "hours": {"1"}, "minutes": {"0"}})
	r.Header.Set("Origin", "https://screen.example.no")
	w := serve(a, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("trusted origin status=%d body=%s", w.Code, w.Body)
	}
}

func TestUnauthenticatedDevicesNeverReceiveAllowance(t *testing.T) {
	a := securedApplication(t)
	for _, token := range []string{"", "invalid", strings.Repeat("a", 64)} {
		w := serve(a, deviceRequest(token, `{"device_id":"pc","user":"child","active_seconds":0}`))
		if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), `"allow"`) {
			t.Fatalf("status=%d body=%s", w.Code, w.Body)
		}
	}
}

func TestDeviceCredentialsBindUserAndMachine(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "child", "pc")
	for _, body := range []string{`{"device_id":"other-pc","user":"child","active_seconds":0}`, `{"device_id":"pc","user":"parent","active_seconds":0}`} {
		w := serve(a, deviceRequest(d.Token, body))
		if w.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", w.Code, w.Body)
		}
	}
	w := serve(a, deviceRequest(d.Token, `{"device_id":"pc","user":"child","active_seconds":0,"heartbeat_id":"first","session_state":"active"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var result response
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Action != "allow" || result.LeaseSeconds <= 0 || result.LeaseSeconds > 90 {
		t.Fatalf("result=%+v", result)
	}
	devices, err := a.service.repository.deviceViews()
	if err != nil || len(devices) != 1 || devices[0].LastSeen == "" {
		t.Fatalf("devices=%+v err=%v", devices, err)
	}
}

func TestEnrollmentIsOneUseAndServerChoosesUser(t *testing.T) {
	a := securedApplication(t)
	code, err := a.service.repository.createPairingCode("child", a.service.now())
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"code":%q,"device_id":"pc","user":"someone-else"}`, code)
	w := serve(a, httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var d enrollmentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.User != "child" || d.DeviceID != "pc" || len(d.Token) != 64 {
		t.Fatalf("credentials=%+v", d)
	}
	w = serve(a, httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(body)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("replay status=%d", w.Code)
	}
	var stored string
	if err := a.service.repository.db.QueryRow("SELECT token_hash FROM devices").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == d.Token || stored != tokenHash(d.Token) {
		t.Fatal("device token must be stored only as hash")
	}
}

func TestPairingCodesExpireAndNewCodeReplacesPrevious(t *testing.T) {
	a := securedApplication(t)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	first, err := a.service.repository.createPairingCode("child", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.service.repository.createPairingCode("child", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.service.repository.enrollDevice(first, "pc", now.Add(time.Minute)); err != errInvalidPairing {
		t.Fatalf("old code err=%v", err)
	}
	if _, err := a.service.repository.enrollDevice(second, "pc", now.Add(16*time.Minute)); err != errInvalidPairing {
		t.Fatalf("expired code err=%v", err)
	}
}

func TestRevocationAndReEnrollmentInvalidateOldToken(t *testing.T) {
	a := securedApplication(t)
	old := enrolledDevice(t, a, "child", "pc")
	current := enrolledDevice(t, a, "child", "pc")
	body := `{"device_id":"pc","user":"child","active_seconds":0}`
	if w := serve(a, deviceRequest(old.Token, body)); w.Code != http.StatusUnauthorized {
		t.Fatalf("old token status=%d", w.Code)
	}
	d, err := a.service.repository.deviceByToken(current.Token)
	if err != nil {
		t.Fatal(err)
	}
	w := serve(a, adminRequest(a, http.MethodPost, a.adminPath+"/devices/revoke", url.Values{"id": {d.ID}}))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("revoke=%d %s", w.Code, w.Body)
	}
	if w := serve(a, deviceRequest(current.Token, body)); w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked status=%d", w.Code)
	}
}

func TestHeartbeatRejectsMalformedOrUnboundedInput(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "child", "pc")
	for _, body := range []string{
		`{`, `null`, `{}`, `{"device_id":"pc","user":"child","active_seconds":-1}`,
		`{"device_id":"pc","user":"child","active_seconds":86401}`,
		`{"device_id":"pc","user":"child","active_seconds":0} {}`,
		`{"device_id":"pc","user":"child","active_seconds":0,"unknown":true}`,
		`{"device_id":"pc","user":"child","session_state":"nonsense"}`,
		`{"device_id":"pc","user":"child","heartbeat_id":"` + strings.Repeat("x", maxRequestBytes) + `"}`,
	} {
		w := serve(a, deviceRequest(d.Token, body))
		if w.Code != http.StatusBadRequest && w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d body=%s", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), `"allow"`) {
			t.Fatal("invalid heartbeat granted permission")
		}
	}
}

func TestDatabaseFailureNeverGrantsAllowance(t *testing.T) {
	a := testApplication(t)
	if err := a.service.repository.close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.heartbeatHandler(w, httptest.NewRequest(http.MethodPost, "/heartbeat", strings.NewReader(`{"device_id":"pc","user":"child","active_seconds":0}`)))
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), `"allow"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
}

func TestPublicLandingHasNoPrivateData(t *testing.T) {
	a := securedApplication(t)
	if err := a.service.setUserQuota("private-child-name", 3600); err != nil {
		t.Fatal(err)
	}
	w := serve(a, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private-child-name") || strings.Contains(w.Body.String(), a.csrfToken) {
		t.Fatalf("public response leaked private state: %d", w.Code)
	}
	for _, header := range []string{"X-Content-Type-Options", "X-Frame-Options", "Content-Security-Policy", "Referrer-Policy"} {
		if w.Header().Get(header) == "" {
			t.Fatalf("missing %s", header)
		}
	}
}

func TestEnrollmentRateLimit(t *testing.T) {
	a := securedApplication(t)
	for i := 0; i < 11; i++ {
		w := serve(a, httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(`{"code":"wrong","device_id":"pc"}`)))
		if i == 10 && (w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "") {
			t.Fatalf("status=%d headers=%v", w.Code, w.Header())
		}
	}
}

func TestRateLimiterWindowAndMemoryBound(t *testing.T) {
	limiter := requestLimiter{limit: 2}
	now := time.Now()
	if !limiter.allow("one", now) || !limiter.allow("one", now) || limiter.allow("one", now) || !limiter.allow("one", now.Add(time.Minute)) {
		t.Fatal("unexpected limiter window")
	}
	for i := 0; i < 5000; i++ {
		limiter.allow(fmt.Sprint(i), now)
	}
	if len(limiter.entries) > 4096 {
		t.Fatalf("unbounded limiter: %d", len(limiter.entries))
	}
}
