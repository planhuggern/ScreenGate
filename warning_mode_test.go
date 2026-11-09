package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestAdminWarningModeWorkflow(t *testing.T) {
	a := securedApplication(t)
	r := httptest.NewRequest(http.MethodPost, a.adminPath+"/user-mode", strings.NewReader("user=child&mode=warning"))
	r.SetBasicAuth(a.adminUser, a.adminPassword)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w := serve(a, r); w.Code != http.StatusForbidden {
		t.Fatalf("mode change without CSRF token: %d", w.Code)
	}
	if w := serve(a, adminRequest(a, http.MethodGet, a.adminPath+"/user-mode", nil)); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("mode change via GET: %d", w.Code)
	}
	if err := a.service.setUserQuota("child", 60); err != nil {
		t.Fatal(err)
	}
	initial, err := a.service.userPolicy("child")
	if err != nil || initial.WarningOnly {
		t.Fatalf("existing users must default to blocking: %+v, %v", initial, err)
	}
	post := func(mode string, status int) {
		t.Helper()
		w := serve(a, adminRequest(a, http.MethodPost, a.adminPath+"/user-mode", url.Values{"user": {"child"}, "mode": {mode}}))
		if w.Code != status {
			t.Fatalf("mode %q: status=%d body=%s", mode, w.Code, w.Body)
		}
	}
	post("warning", http.StatusSeeOther)
	warning, err := a.service.userPolicy("child")
	if err != nil || !warning.WarningOnly || warning.PolicyVersion != initial.PolicyVersion+1 {
		t.Fatalf("mode was not saved and versioned: %+v, %v", warning, err)
	}
	post("warning", http.StatusSeeOther)
	post("invalid", http.StatusBadRequest)
	post("", http.StatusBadRequest)
	unchanged, err := a.service.userPolicy("child")
	if err != nil || !unchanged.WarningOnly || unchanged.PolicyVersion != warning.PolicyVersion {
		t.Fatalf("unchanged/invalid submission changed policy: %+v, %v", unchanged, err)
	}
	if err := a.service.setUserPaused("child", true); err != nil {
		t.Fatal(err)
	}
	paused, _ := a.service.userPolicy("child")
	if !paused.WarningOnly || !paused.Paused {
		t.Fatal("pause overwrote the enforcement mode")
	}
	w := serve(a, adminRequest(a, http.MethodGet, a.adminPath, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `value="warning" selected`) || !strings.Contains(w.Body.String(), "Kun advarsel") {
		t.Fatalf("dashboard did not render saved mode: %d %s", w.Code, w.Body)
	}
	post("block", http.StatusSeeOther)
	blocking, _ := a.service.userPolicy("child")
	if blocking.WarningOnly || !blocking.Paused || blocking.PolicyVersion != paused.PolicyVersion+1 {
		t.Fatalf("switching back lost pause or version: %+v", blocking)
	}
	events, err := a.recentAudit()
	if err != nil || len(events) == 0 || events[0].Description != "Modus endret til Blokker" {
		t.Fatalf("mode change missing from audit: %+v, %v", events, err)
	}
}

func TestWarningModeMigrationAndDatabaseRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	goose.SetBaseFS(migrationFiles)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 8); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO user_settings(user, paused) VALUES ('child', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err := openRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.close()
	policy, err := repo.userPolicy("child", "2026-11-09")
	if err != nil || policy.WarningOnly || !policy.Paused {
		t.Fatalf("upgrade must retain pause and default to blocking: %+v %v", policy, err)
	}
	if err := repo.setUserWarningOnly("child", true); err != nil {
		t.Fatal(err)
	}
	if err := repo.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	policy, err = reopened.userPolicy("child", "2026-11-09")
	if err != nil || !policy.WarningOnly || !policy.Paused {
		t.Fatalf("mode must survive server restart: %+v %v", policy, err)
	}
}

func TestHeartbeatPublishesWarningModeAndKeepsCountingOverQuota(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "child", "pc")
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	a.service.now = func() time.Time { return now }
	a.service.location = time.UTC
	if err := a.service.setUserQuota(d.User, 60); err != nil {
		t.Fatal(err)
	}
	for index, mode := range []string{"warning", "block"} {
		w := serve(a, adminRequest(a, http.MethodPost, a.adminPath+"/user-mode", url.Values{"user": {d.User}, "mode": {mode}}))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("change mode: %d %s", w.Code, w.Body)
		}
		h := measuredHeartbeat(d.User, d.DeviceID, mode, mode, now.Add(-time.Minute), now)
		body, err := json.Marshal(h)
		if err != nil {
			t.Fatal(err)
		}
		w = serve(a, deviceRequest(d.Token, string(body)))
		var got response
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
			t.Fatalf("heartbeat: %d %v %s", w.Code, err, w.Body)
		}
		if got.Policy == nil || got.Policy.WarningOnly != (mode == "warning") || got.Action != "lock" || got.Reason != "quota" || got.RemainingSeconds != 0 {
			t.Fatalf("mode or quota missing from response: %+v", got)
		}
		total, err := a.service.dailyTotal(d.User, "2026-11-09")
		if err != nil || total != (index+1)*60 {
			t.Fatalf("usage must continue beyond quota: total=%d err=%v", total, err)
		}
		now = now.Add(time.Minute)
	}
}
