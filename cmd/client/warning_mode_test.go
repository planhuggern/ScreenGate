package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"screengate/internal/screenpolicy"
)

func TestWarningModeEnforcement(t *testing.T) {
	for _, warningOnly := range []bool{false, true} {
		for _, reason := range []string{"quota", "schedule", "paused", "authentication_failed"} {
			name := reason + "/block"
			if warningOnly {
				name = reason + "/warning"
			}
			t.Run(name, func(t *testing.T) {
				now := osloTime(t, "2026-11-09 20:00:00")
				rules := screenpolicy.Rules{DailyQuotaSeconds: 600, WarningOnly: warningOnly}
				remaining := 100
				switch reason {
				case "quota":
					remaining = 0
				case "schedule":
					rules.Weekdays = []screenpolicy.Weekday{{Weekday: int(time.Monday), Configured: true, QuotaSeconds: 600, EndMinute: 20 * 60}}
				case "paused":
					rules.Paused = true
				}
				state := cachedState(t, now, rules, remaining)
				if reason == "authentication_failed" {
					state.invalidate(reason)
				}
				locks := 0
				visible := false
				client := clientLoop{state: state, sessionState: "active", guard: enforcement{enabled: true}, runtime: clientRuntime{
					lock:              func() { locks++ },
					setExpiredWarning: func(show bool) error { visible = show; return nil },
				}}
				client.enforce(now)
				if (locks == 0) != warningOnly {
					t.Fatalf("locks=%d warningOnly=%t", locks, warningOnly)
				}
				wantVisible := reason == "quota" || reason == "schedule" || (warningOnly && reason == "paused")
				if visible != wantVisible {
					t.Fatalf("visible=%t want=%t", visible, wantVisible)
				}
				client.sessionState = "locked"
				before := locks
				client.enforce(now.Add(4 * time.Second))
				if locks != before || visible != wantVisible {
					t.Fatal("locked session changed the warning or attempted another lock")
				}
			})
		}
	}
}

func TestLoopChangesWarningModeAndClearsWarningWhenTimeIsGranted(t *testing.T) {
	now := testMoment()
	h := startTestClient(t, clientState{}, now, "", true)
	rules := screenpolicy.Rules{DailyQuotaSeconds: 120, WarningOnly: true, BonusDate: now.Format("2006-01-02")}
	reply := func(version, remaining int) loopReply {
		action, reason := "lock", "quota"
		if remaining > 0 {
			action, reason = "allow", "allowed"
		}
		data, err := json.Marshal(response{
			Policy: &rules, Action: action, Reason: reason, PolicyVersion: version,
			QuotaSeconds: 120, RemainingSeconds: remaining, LeaseSeconds: remaining,
			PolicyDate: rules.BonusDate, ServerTime: h.time(), Timezone: "UTC",
		})
		if err != nil {
			t.Fatal(err)
		}
		return loopReply{status: 200, body: string(data)}
	}
	h.request().reply <- reply(1, 0)
	h.await(func(s clientState) bool { return s.PolicyVersion == 1 && s.Report == nil })
	if !h.expiredWarning.Load() || h.locks.Load() != 0 {
		t.Fatal("warning mode must show the expiry message without locking")
	}
	h.poll(4 * time.Second)
	if !h.expiredWarning.Load() || h.locks.Load() != 0 {
		t.Fatal("expiry message must remain visible without later locking")
	}
	rules.WarningOnly = false
	h.send(h.reports)
	h.request().reply <- reply(2, 0)
	h.await(func(s clientState) bool { return s.PolicyVersion == 2 && s.Report == nil })
	if h.locks.Load() == 0 || !h.expiredWarning.Load() {
		t.Fatal("switching to blocking must enforce immediately and retain the message")
	}
	before := h.locks.Load()
	rules.WarningOnly = true
	h.send(h.reports)
	h.request().reply <- reply(3, 0)
	h.await(func(s clientState) bool { return s.PolicyVersion == 3 && s.Report == nil })
	h.poll(4 * time.Second)
	if h.locks.Load() != before || !h.expiredWarning.Load() {
		t.Fatal("switching to warning-only must stop locking and keep the warning")
	}
	h.send(h.reports)
	h.request().reply <- reply(4, 60)
	h.await(func(s clientState) bool { return s.PolicyVersion == 4 && s.Report == nil })
	if h.expiredWarning.Load() || h.locks.Load() != before {
		t.Fatal("granting time must clear the warning and stop locking")
	}
	h.send(h.reports)
	h.request().reply <- reply(5, 0)
	h.await(func(s clientState) bool { return s.PolicyVersion == 5 && s.Report == nil })
	if !h.expiredWarning.Load() {
		t.Fatal("expiry must show the message again")
	}
	h.stop()
	if h.expiredWarning.Load() {
		t.Fatal("stopping the client left its persistent warning visible")
	}
}

func TestLoopWarningModeCountsThroughLocalExpiry(t *testing.T) {
	now := osloTime(t, "2026-11-09 12:00:00")
	state := cachedState(t, now, screenpolicy.Rules{DailyQuotaSeconds: 600, WarningOnly: true}, 2)
	h := startTestClient(t, state, now, "", true)
	h.request().reply <- loopReply{status: 503, body: `{}`}
	h.await(func(s clientState) bool { return s.ServerUnavailable })
	h.poll(time.Second)
	if h.expiredWarning.Load() {
		t.Fatal("expiry message appeared before time ran out")
	}
	h.poll(time.Second)
	if !h.expiredWarning.Load() || h.locks.Load() != 0 {
		t.Fatal("local quota expiry must show the warning without locking")
	}
	after := h.poll(time.Second)
	if !h.expiredWarning.Load() || h.locks.Load() != 0 || after.unacknowledgedUsage("2026-11-09") != 3 {
		t.Fatal("expired warning mode must continue counting without locking")
	}
}

func TestWarningModeSurvivesOfflineExpiryAndRestart(t *testing.T) {
	now := osloTime(t, "2026-11-09 12:00:00")
	state := cachedState(t, now, screenpolicy.Rules{DailyQuotaSeconds: 600, WarningOnly: true}, 2)
	state.account(2, now.Add(2*time.Second))
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, "token", state); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	restored, err := loadState(path, "client", "token", now)
	if err != nil {
		t.Fatal(err)
	}
	visible := false
	client := clientLoop{state: restored, sessionState: "active", guard: enforcement{enabled: true}, runtime: clientRuntime{
		lock:              func() { t.Fatal("offline restart must preserve warning mode") },
		setExpiredWarning: func(show bool) error { visible = show; return nil },
	}}
	client.enforce(now)
	if !visible || client.state.RemainingSeconds != 0 {
		t.Fatal("offline restart lost the warning or refilled the quota")
	}
	// Usage is still measured after expiry and must be queued for reconciliation.
	client.state.account(5, now.Add(5*time.Second))
	if client.state.unacknowledgedUsage("2026-11-09") != 7 {
		t.Fatal("warning mode stopped recording usage after quota expiry")
	}
}
