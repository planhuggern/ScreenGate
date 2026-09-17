package main

import (
	"database/sql"
	"encoding/csv"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminControlWorkflow(t *testing.T) {
	a := securedApplication(t)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	a.service.location = time.UTC
	a.service.now = func() time.Time { return now }
	post := func(path string, form url.Values) {
		t.Helper()
		w := serve(a, adminRequest(a, http.MethodPost, a.adminPath+path, form))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body)
		}
	}
	post("/user-quota", url.Values{"user": {"child"}, "hours": {"1"}, "minutes": {"0"}})
	post("/user-policy", url.Values{"user": {"child"}, "day_5_enabled": {"on"}, "day_5_hours": {"2"}, "day_5_minutes": {"0"}, "day_5_start": {"13:00"}, "day_5_end": {"20:00"}})
	policy, err := a.service.userPolicy("child")
	if err != nil {
		t.Fatal(err)
	}
	if decision := evaluatePolicy(policy, 0, now); decision.Reason != "schedule" {
		t.Fatalf("decision=%+v", decision)
	}
	post("/user-bonus", url.Values{"user": {"child"}, "action": {"add"}, "minutes": {"15"}})
	now = now.Add(time.Hour)
	policy, _ = a.service.userPolicy("child")
	if decision := evaluatePolicy(policy, 0, now); decision.RemainingSeconds != 8100 || decision.Action != "allow" {
		t.Fatalf("decision=%+v", decision)
	}
	post("/user-pause", url.Values{"user": {"child"}, "paused": {"true"}})
	policy, _ = a.service.userPolicy("child")
	if decision := evaluatePolicy(policy, 0, now); decision.Reason != "paused" {
		t.Fatalf("decision=%+v", decision)
	}
	post("/user-pause", url.Values{"user": {"child"}, "paused": {"false"}})
	post("/user-bonus", url.Values{"user": {"child"}, "action": {"reset"}})
	policy, _ = a.service.userPolicy("child")
	if policy.Paused || policy.BonusSeconds != 0 {
		t.Fatalf("policy=%+v", policy)
	}
	post("/user-policy", url.Values{"user": {"child"}}) // Inherit base quota again.
	policy, _ = a.service.userPolicy("child")
	if decision := evaluatePolicy(policy, 0, now); decision.QuotaSeconds != 3600 {
		t.Fatalf("decision=%+v", decision)
	}
	events, err := a.recentAudit()
	if err != nil || len(events) != 7 {
		t.Fatalf("audit len=%d err=%v", len(events), err)
	}
}

func TestAdminValidationDoesNotChangePolicy(t *testing.T) {
	a := securedApplication(t)
	if err := a.service.setUserQuota("child", 3600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path string
		form url.Values
	}{
		{"/user-quota", url.Values{"hours": {"999999999999999999999999"}, "minutes": {"0"}}},
		{"/user-quota", url.Values{"hours": {"24"}, "minutes": {"1"}}},
		{"/user-quota", url.Values{"hours": {"-1"}, "minutes": {"0"}}},
		{"/user-pause", url.Values{"paused": {"perhaps"}}},
		{"/user-bonus", url.Values{"minutes": {"-15"}}},
		{"/user-bonus", url.Values{"action": {"unknown"}, "minutes": {"15"}}},
		{"/user-policy", url.Values{"day_1_enabled": {"on"}, "day_1_hours": {"1"}, "day_1_minutes": {"0"}, "day_1_start": {"12:00"}, "day_1_end": {"12:00"}}},
		{"/user-policy", url.Values{"day_1_enabled": {"on"}, "day_1_hours": {"1"}, "day_1_minutes": {"0"}, "day_1_start": {"25:00"}, "day_1_end": {"24:00"}}},
	}
	for _, test := range tests {
		test.form.Set("user", "child")
		w := serve(a, adminRequest(a, http.MethodPost, a.adminPath+test.path, test.form))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s returned %d: %s", test.path, w.Code, w.Body)
		}
	}
	policy, err := a.service.userPolicy("child")
	if err != nil || policy.DailyQuotaSeconds != 3600 || policy.BonusSeconds != 0 || policy.Paused || policy.PolicyVersion != 1 {
		t.Fatalf("changed policy=%+v err=%v", policy, err)
	}
}

func TestPairingStartsNewUsersWithBoundedQuota(t *testing.T) {
	a := securedApplication(t)
	for _, user := range []string{"new-user", "existing"} {
		if user == "existing" {
			if err := a.service.setUserQuota(user, 7200); err != nil {
				t.Fatal(err)
			}
		}
		w := serve(a, adminRequest(a, http.MethodPost, a.adminPath+"/devices/pair", url.Values{"user": {user}}))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "pair-code") {
			t.Fatalf("pairing status=%d", w.Code)
		}
		quota, err := a.service.repository.userQuota(user)
		want := 3600
		if user == "existing" {
			want = 7200
		}
		if err != nil || quota != want {
			t.Fatalf("%s quota=%d err=%v", user, quota, err)
		}
	}
}

func TestCSVExportEscapesSpreadsheetFormulas(t *testing.T) {
	a := securedApplication(t)
	if err := a.service.setUserQuota("=1+1", 3600); err != nil {
		t.Fatal(err)
	}
	w := serve(a, adminRequest(a, http.MethodGet, a.adminPath+"/export.csv", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	rows, err := csv.NewReader(w.Body).ReadAll()
	if err != nil || len(rows) != 8 || rows[1][1] != "'=1+1" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestBackupContainsConsistentDatabaseSnapshot(t *testing.T) {
	a := securedApplication(t)
	if err := a.service.setUserQuota("child", 5400); err != nil {
		t.Fatal(err)
	}
	d := enrolledDevice(t, a, "child", "pc")
	w := serve(a, adminRequest(a, http.MethodGet, a.adminPath+"/backup", nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "SQLite format 3") {
		t.Fatalf("backup status=%d body=%s", w.Code, w.Body)
	}
	backupPath := filepath.Join(t.TempDir(), "backup.db")
	file, err := os.Create(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(file, w.Body); err != nil {
		t.Fatal(err)
	}
	file.Close()
	db, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var quota int
	if err := db.QueryRow("SELECT daily_quota_seconds FROM user_quotas WHERE user='child'").Scan(&quota); err != nil || quota != 5400 {
		t.Fatalf("quota=%d err=%v", quota, err)
	}
	var stored string
	if err := db.QueryRow("SELECT token_hash FROM devices").Scan(&stored); err != nil || stored != tokenHash(d.Token) {
		t.Fatal("backup did not preserve device registration")
	}
}

func TestConfigurationRequiresPasswordAndValidRoutes(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "")
	if _, err := loadConfiguration(); err == nil {
		t.Fatal("unprotected startup accepted")
	}
	t.Setenv("ADMIN_PASSWORD", "long-test-password")
	t.Setenv("ADMIN_PATH", "/heartbeat")
	if _, err := loadConfiguration(); err == nil {
		t.Fatal("API path collision accepted")
	}
	t.Setenv("ADMIN_PATH", "/family-admin")
	t.Setenv("SCREENGATE_TIMEZONE", "Europe/Oslo")
	c, err := loadConfiguration()
	if err != nil || c.location.String() != "Europe/Oslo" {
		t.Fatalf("configuration=%+v err=%v", c, err)
	}
	t.Setenv("SCREENGATE_TIMEZONE", "not-a-timezone")
	if _, err := loadConfiguration(); err == nil {
		t.Fatal("invalid timezone accepted")
	}
}

func TestLoginThrottleCannotBeBypassedByTryingCorrectPassword(t *testing.T) {
	a := securedApplication(t)
	for i := 0; i < 10; i++ {
		r := adminRequest(a, http.MethodGet, a.adminPath, nil)
		r.SetBasicAuth(a.adminUser, "wrong")
		serve(a, r)
	}
	if w := serve(a, adminRequest(a, http.MethodGet, a.adminPath, nil)); w.Code != http.StatusTooManyRequests {
		t.Fatalf("throttled address status=%d", w.Code)
	}
	r := adminRequest(a, http.MethodGet, a.adminPath, nil)
	r.RemoteAddr = "192.0.2.2:1234"
	if w := serve(a, r); w.Code != http.StatusOK {
		t.Fatalf("independent address status=%d", w.Code)
	}
}
