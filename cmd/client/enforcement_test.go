package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTestModeNeverCallsLockAndKeepsUsage(t *testing.T) {
	now := testMoment()
	state := clientState{}
	state.apply(response{Action: "lock", Reason: "quota_exhausted"}, now, now)
	guard := enforcement{}
	logs := 0
	lock := func() { t.Fatal("test mode called workstation lock") }
	for i := 1; i <= 60; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		state.account(1, at)
		guard.poll(!state.allowed(at), at, lock, func() { logs++ })
	}
	report, err := state.prepareReport("pc", "child", "active", now.Add(time.Minute))
	if err != nil || report.ActiveSeconds != 60 || report.SessionState != "active" || logs != 1 {
		t.Fatalf("report=%+v logs=%d err=%v", report, logs, err)
	}
	for _, reason := range []string{"state_unavailable", "authentication_failed", "clock_changed"} {
		state.invalidate(reason)
		guard.poll(!state.allowed(now), now, lock, func() {})
	}
}

func TestEnforcementRequiresOptInAndThrottlesLock(t *testing.T) {
	guard := enforcement{enabled: true}
	locks := 0
	now := testMoment()
	for i := 0; i < 6; i++ {
		guard.poll(true, now.Add(time.Duration(i)*time.Second), func() { locks++ }, func() { t.Fatal("unexpected test-mode log") })
	}
	guard.poll(false, now.Add(time.Minute), func() { t.Fatal("allowed session locked") }, func() {})
	if locks != 2 {
		t.Fatalf("locks=%d", locks)
	}
}

func TestOldConfigurationDefaultsToTestMode(t *testing.T) {
	var config clientConfig
	if err := json.Unmarshal([]byte(`{"server":"https://example.test/heartbeat","token":"secret"}`), &config); err != nil {
		t.Fatal(err)
	}
	if config.EnableLocking {
		t.Fatal("old configuration enabled locking")
	}
}
