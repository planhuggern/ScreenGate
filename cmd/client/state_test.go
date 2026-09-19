package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testMoment() time.Time { return time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC) }

func TestFreshClientAllowsUntilServerAuthorization(t *testing.T) {
	state, err := loadState(filepath.Join(t.TempDir(), "missing.json"), "identity", "secret", testMoment())
	if err != nil || !state.allowed(testMoment()) || state.ServerContacted {
		t.Fatalf("fresh state allowed=%v, contacted=%v, err=%v", state.allowed(testMoment()), state.ServerContacted, err)
	}
}

func TestExplicitServerLockIsStillEnforcedOffline(t *testing.T) {
	now := testMoment()
	state := clientState{}
	state.apply(response{Action: "lock", Reason: "quota_exhausted", LeaseSeconds: 90}, now, now)
	if state.allowed(now.Add(time.Second)) {
		t.Fatal("explicit server lock was bypassed")
	}
}

func TestServerUnavailableFailsOpen(t *testing.T) {
	now := testMoment()
	state := clientState{}
	state.apply(response{Action: "lock", Reason: "quota_exhausted", LeaseSeconds: 90}, now, now)
	state.markServerUnavailable()
	if !state.allowed(now.Add(time.Hour)) || state.Reason != "server_unavailable" {
		t.Fatal("server outage did not fail open")
	}
}

func TestOfflineLeaseSurvivesRestartWithoutRefill(t *testing.T) {
	now := testMoment()
	state := clientState{Identity: "identity"}
	state.apply(response{Action: "allow", QuotaSeconds: 600, RemainingSeconds: 50, LeaseSeconds: 90}, now, now)
	state.account(20, now.Add(20*time.Second))
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, "secret", state); err != nil {
		t.Fatal(err)
	}
	restarted, err := loadState(path, "identity", "secret", now.Add(25*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if restarted.RemainingSeconds != 30 || restarted.PendingSeconds != 20 || !restarted.allowed(now.Add(25*time.Second)) {
		t.Fatalf("unexpected restored state: %+v", restarted)
	}
	if restarted.allowed(now.Add(50 * time.Second)) {
		t.Fatal("restart extended original lease")
	}
}

func TestOfflineAuthorizationExpiresForUnlimitedUsers(t *testing.T) {
	now := testMoment()
	state := clientState{}
	state.apply(response{Action: "allow", LeaseSeconds: 86400}, now, now)
	if !state.allowed(now.Add(119*time.Second)) || state.allowed(now.Add(120*time.Second)) {
		t.Fatal("unlimited policy escaped maximum offline lease")
	}
}

func TestOfflineAuthorizationTracksActiveQuotaAndClockRollback(t *testing.T) {
	now := testMoment()
	state := clientState{}
	state.apply(response{Action: "allow", QuotaSeconds: 600, RemainingSeconds: 10, LeaseSeconds: 90}, now, now)
	state.account(10, now.Add(time.Second))
	if state.allowed(now.Add(time.Second)) {
		t.Fatal("consumed quota still allowed")
	}
	state.apply(response{Action: "allow", LeaseSeconds: 90}, now, now)
	if state.allowed(now.Add(-time.Nanosecond)) || state.Reason != "clock_changed" {
		t.Fatal("backward clock kept authorization")
	}
}

func TestRetriesKeepReportIDAndDoNotLoseNewUsage(t *testing.T) {
	now := testMoment()
	state := clientState{PolicyDate: "2026-09-18"}
	state.account(20, now)
	first, err := state.prepareReport("pc", "child", "active", now)
	if err != nil {
		t.Fatal(err)
	}
	state.account(8, now.Add(8*time.Second))
	retry, err := state.prepareReport("pc", "child", "locked", now.Add(8*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if first.HeartbeatID == "" || retry.HeartbeatID != first.HeartbeatID || retry.ActiveSeconds != 20 || retry.ActivityDate != "2026-09-18" || state.PendingSeconds != 8 {
		t.Fatalf("retry=%+v state=%+v", retry, state)
	}
	state.apply(response{Action: "allow", PolicyDate: "2026-09-18", QuotaSeconds: 600, RemainingSeconds: 100, LeaseSeconds: 90}, now, now.Add(8*time.Second))
	if state.RemainingSeconds != 92 || state.Report != nil {
		t.Fatalf("response lost in-flight use: %+v", state)
	}
	next, _ := state.prepareReport("pc", "child", "active", now.Add(9*time.Second))
	if next.ActiveSeconds != 8 || next.HeartbeatID == first.HeartbeatID {
		t.Fatalf("next report=%+v", next)
	}
}

func TestMidnightPendingUsageKeepsOriginalPolicyDate(t *testing.T) {
	now := testMoment()
	state := clientState{PolicyDate: "2026-09-18"}
	state.account(7, now)
	state.apply(response{Action: "allow", PolicyDate: "2026-09-19", QuotaSeconds: 600, RemainingSeconds: 600, LeaseSeconds: 90}, now, now)
	if state.RemainingSeconds != 600 {
		t.Fatal("yesterday's unreported usage reduced today's local quota")
	}
	report, _ := state.prepareReport("pc", "child", "active", now)
	if report.ActivityDate != "2026-09-18" || report.ActiveSeconds != 7 {
		t.Fatalf("lost previous day: %+v", report)
	}
	state.account(3, now.Add(3*time.Second))
	if state.PendingDate != "2026-09-19" || state.PendingSeconds != 3 {
		t.Fatalf("new-day pending state=%+v", state)
	}
}

func TestStateRejectsDifferentIdentityTokenAndTampering(t *testing.T) {
	now := testMoment()
	path := filepath.Join(t.TempDir(), "state.json")
	state := clientState{Identity: "one", Action: "allow", ServerContacted: true, UpdatedAt: now, LeaseExpiresAt: now.Add(time.Minute)}
	if err := saveState(path, "secret", state); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"two", "secret"}, {"one", "different"}} {
		loaded, err := loadState(path, pair[0], pair[1], now)
		if err == nil || loaded.allowed(now) {
			t.Fatal("foreign state accepted")
		}
	}
	if err := os.WriteFile(path, []byte(`{"state":{"identity":"one","action":"allow"},"mac":"bad"}`), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(path, "one", "secret", now)
	if err == nil || loaded.allowed(now) {
		t.Fatal("modified state accepted")
	}
}

func TestLeaseDoesNotGrowDuringNetworkDelayOrPastTransition(t *testing.T) {
	now := testMoment()
	state := clientState{}
	state.apply(response{Action: "allow", LeaseSeconds: 90, ServerTime: now, NextTransitionAt: now.Add(10 * time.Second)}, now, now.Add(5*time.Second))
	if !state.allowed(now.Add(9*time.Second)) || state.allowed(now.Add(10*time.Second)) {
		t.Fatal("lease crossed schedule transition")
	}
	state.apply(response{Action: "allow", LeaseSeconds: 3}, now, now.Add(5*time.Second))
	if state.allowed(now.Add(5 * time.Second)) {
		t.Fatal("late response renewed already expired lease")
	}
}

func TestDurableInflightReportRestoredAfterRestart(t *testing.T) {
	now := testMoment()
	state := clientState{Identity: "one", Action: "lock", UpdatedAt: now, PendingSeconds: 25}
	first, _ := state.prepareReport("pc", "child", "active", now)
	state.account(4, now.Add(4*time.Second))
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, "secret", state); err != nil {
		t.Fatal(err)
	}
	restored, err := loadState(path, "one", "secret", now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	retry, _ := restored.prepareReport("pc", "child", "active", now.Add(5*time.Second))
	if retry.HeartbeatID != first.HeartbeatID || retry.ActiveSeconds != 25 || restored.PendingSeconds != 4 {
		t.Fatal("restart lost pending or in-flight usage")
	}
	// Replacing an existing state file must work on Windows too.
	if err := saveState(path, "secret", restored); err != nil {
		t.Fatal(err)
	}
}

func TestWarningsUseEarliestQuotaOrScheduledLock(t *testing.T) {
	now := testMoment()
	state := clientState{}
	state.apply(response{Action: "allow", LeaseSeconds: 90, QuotaSeconds: 7200, RemainingSeconds: 3600, ServerTime: now, NextLockAt: now.Add(5 * time.Minute)}, now, now)
	if remaining := state.warningRemaining(now); remaining != 300 {
		t.Fatalf("warning=%d", remaining)
	}
	state.RemainingSeconds = 60
	if state.warningRemaining(now) != 60 {
		t.Fatal("quota must win over later bedtime")
	}
	state.QuotaSeconds = 0
	if state.warningRemaining(now) != 300 {
		t.Fatal("unlimited quota still needs bedtime warnings")
	}
	state.ScheduledLockAt = time.Time{}
	if state.warningRemaining(now) <= 900 {
		t.Fatal("unlimited unscheduled time must not warn")
	}
}
