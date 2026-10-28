package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"screengate/internal/screenpolicy"
)

func osloTime(t *testing.T, date string) time.Time {
	t.Helper()
	location, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.ParseInLocation("2006-01-02 15:04:05", date, location)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func cachedState(t *testing.T, at time.Time, rules screenpolicy.Rules, remaining int) clientState {
	t.Helper()
	state := clientState{Identity: "client", UsageStarted: true}
	rules.BonusDate = at.Format("2006-01-02")
	state.apply(response{
		Policy: &rules, Action: "allow", QuotaSeconds: rules.QuotaAt(at), RemainingSeconds: remaining,
		PolicyDate: rules.BonusDate, ServerTime: at, Timezone: "Europe/Oslo", LeaseSeconds: 90,
	}, at, at)
	state.markServerUnavailable()
	return state
}

func TestOfflineMidnightUsesNextWeekdayAndDropsBonus(t *testing.T) {
	at := osloTime(t, "2026-10-28 23:59:50")
	state := cachedState(t, at, screenpolicy.Rules{
		DailyQuotaSeconds: 600, BonusSeconds: 300,
		Weekdays: []screenpolicy.Weekday{{Weekday: int(time.Thursday), Configured: true, QuotaSeconds: 120, EndMinute: 1440}},
	}, 20)
	state.account(20, at.Add(20*time.Second)) // Ten seconds on each side of midnight.
	if !state.allowed(at.Add(20*time.Second)) || state.QuotaSeconds != 120 || state.RemainingSeconds != 110 || state.PolicyDate != "2026-10-29" {
		t.Fatalf("new day should have 110 of Thursday's 120 seconds: %+v", state)
	}
	if state.LocalDayUsage["2026-10-28"] != 10 || state.LocalDayUsage["2026-10-29"] != 10 {
		t.Fatalf("use split over wrong days: %v", state.LocalDayUsage)
	}
	if state.unacknowledgedUsage("2026-10-28") != 10 || state.unacknowledgedUsage("2026-10-29") != 10 {
		t.Fatal("reconnection would charge the wrong day")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, "token", state); err != nil {
		t.Fatal(err)
	}
	restored, err := loadState(path, "client", "token", at.Add(time.Hour))
	if err != nil || restored.RemainingSeconds != 110 || !restored.allowed(at.Add(time.Hour)) {
		t.Fatalf("restart refilled or lost quota: remaining=%d err=%v", restored.RemainingSeconds, err)
	}
	restored.account(110, at.Add(time.Hour+110*time.Second))
	if restored.allowed(at.Add(time.Hour+110*time.Second)) || restored.Reason != "quota" {
		t.Fatal("offline daily quota was not exhausted")
	}
}

func TestOfflineScheduleClosesAndReopensWithoutQuotaRefill(t *testing.T) {
	at := osloTime(t, "2026-10-28 19:59:58")
	state := cachedState(t, at, screenpolicy.Rules{DailyQuotaSeconds: 600, Weekdays: []screenpolicy.Weekday{
		{Weekday: int(time.Wednesday), Configured: true, QuotaSeconds: 600, StartMinute: 18 * 60, EndMinute: 20 * 60},
		{Weekday: int(time.Thursday), Configured: true, QuotaSeconds: 300, StartMinute: 18 * 60, EndMinute: 20 * 60},
	}}, 40)
	state.account(2, at.Add(2*time.Second))
	if state.allowed(at.Add(2*time.Second)) || state.Reason != "schedule" || state.RemainingSeconds != 38 {
		t.Fatal("window did not close at 20:00")
	}
	next := osloTime(t, "2026-10-29 17:59:59")
	state.account(0, next) // Locked/sleeping time is not usage.
	if state.allowed(next) || state.RemainingSeconds != 300 {
		t.Fatal("new quota should remain blocked until Thursday's window")
	}
	if !state.allowed(next.Add(time.Second)) || state.RemainingSeconds != 300 {
		t.Fatal("Thursday's window did not reopen")
	}
	state.account(12, next.Add(13*time.Second))
	if !state.allowed(next.Add(time.Minute)) || state.RemainingSeconds != 288 {
		t.Fatal("checking a window returned spent time")
	}
}

func TestOfflineOvernightWindowCannotOverrideDisabledDay(t *testing.T) {
	at := osloTime(t, "2026-10-28 23:59:59")
	state := cachedState(t, at, screenpolicy.Rules{DailyQuotaSeconds: 600, Weekdays: []screenpolicy.Weekday{
		{Weekday: int(time.Wednesday), Configured: true, QuotaSeconds: 600, StartMinute: 22 * 60, EndMinute: 2 * 60},
		{Weekday: int(time.Thursday), Configured: true, Disabled: true, EndMinute: 1440},
	}}, 100)
	if !state.allowed(at) || state.allowed(at.Add(time.Second)) || state.Reason != "schedule" {
		t.Fatal("overnight window overruled the disabled next day")
	}
}

func TestOfflinePauseAndLocalErrorsSurviveNewDay(t *testing.T) {
	at := osloTime(t, "2026-10-28 23:59:59")
	for _, reason := range []string{"paused", "authentication_failed", "state_unavailable", "clock_changed"} {
		t.Run(reason, func(t *testing.T) {
			state := cachedState(t, at, screenpolicy.Rules{DailyQuotaSeconds: 600, Paused: reason == "paused"}, 100)
			if reason != "paused" {
				state.invalidate(reason)
			}
			if state.allowed(at.Add(time.Hour)) || state.Reason != reason {
				t.Fatalf("new day lifted %s: %+v", reason, state)
			}
		})
	}
}

func TestOfflineUnlimitedAndSleepAcrossSeveralDays(t *testing.T) {
	at := osloTime(t, "2026-10-28 23:59:59")
	state := cachedState(t, at, screenpolicy.Rules{DailyQuotaSeconds: 600, BonusSeconds: 300, Weekdays: []screenpolicy.Weekday{
		{Weekday: int(time.Saturday), Configured: true, QuotaSeconds: 0, EndMinute: 1440},
	}}, 10)
	saturday := osloTime(t, "2026-10-31 12:00:00")
	state.account(0, saturday)
	state.account(1000, saturday.Add(1000*time.Second))
	if !state.allowed(saturday.Add(1000*time.Second)) || state.QuotaSeconds != 0 {
		t.Fatal("zero quota must remain unlimited")
	}
	sunday := osloTime(t, "2026-11-01 12:00:00")
	state.account(0, sunday)
	if !state.allowed(sunday) || state.RemainingSeconds != 600 {
		t.Fatal("sleep should neither consume quota nor carry bonus into Sunday")
	}
}

func TestOfflineMidnightUsesServerTimezoneAcrossDST(t *testing.T) {
	for _, date := range []string{"2026-03-29 00:00:00", "2026-10-25 00:00:00"} {
		t.Run(date, func(t *testing.T) {
			at := osloTime(t, date)
			// The PC runs UTC with its clock seven hours behind the server.
			pc := at.UTC().Add(-7 * time.Hour)
			state := cachedState(t, at, screenpolicy.Rules{DailyQuotaSeconds: 600}, 10)
			state.ServerClockOffset = 7 * time.Hour
			state.UpdatedAt = pc
			midnight := screenpolicy.MidnightAfter(at)
			state.account(0, midnight.Add(-7*time.Hour-time.Second))
			if state.PolicyDate != at.Format("2006-01-02") || state.RemainingSeconds != 10 {
				t.Fatal("reset happened before the server's calendar midnight")
			}
			state.account(0, midnight.Add(-7*time.Hour))
			if state.PolicyDate != midnight.Format("2006-01-02") || state.RemainingSeconds != 600 {
				t.Fatal("missed actual midnight on a 23/25-hour day")
			}
		})
	}
}

func TestServerRemainsMasterAndOlderRepliesCannotRefillNextDay(t *testing.T) {
	at := osloTime(t, "2026-10-28 23:59:59")
	rules := screenpolicy.Rules{DailyQuotaSeconds: 600}
	state := cachedState(t, at, rules, 40)
	state.account(11, at.Add(11*time.Second))
	batch, err := state.prepareReport("pc", "child", "active", at.Add(11*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	// An acknowledged batch may include a few seconds beyond the server's
	// decision time. A late reply for yesterday must retain those seconds.
	state.apply(response{Policy: state.CachedPolicy, Action: "allow", UsageAck: batch.Usage.ID,
		PolicyDate: "2026-10-28", QuotaSeconds: 600, RemainingSeconds: 39,
		ServerTime: at, Timezone: "Europe/Oslo"}, at, at.Add(11*time.Second))
	if state.RemainingSeconds != 590 || state.PolicyDate != "2026-10-29" || state.PendingUsage != nil {
		t.Fatalf("late reply refilled next day: %+v", state)
	}
	// On today's response, the server's balance includes other PCs too.
	now := at.Add(12 * time.Second)
	state.apply(response{Policy: state.CachedPolicy, Action: "allow", PolicyDate: "2026-10-29",
		QuotaSeconds: 600, RemainingSeconds: 70, ServerTime: now, Timezone: "Europe/Oslo"}, now, now)
	if !state.allowed(now.Add(time.Minute)) || state.RemainingSeconds != 70 {
		t.Fatal("local rules overruled the shared server balance")
	}
}

func TestOlderServerDecisionReplacesCachedPolicy(t *testing.T) {
	at := osloTime(t, "2026-10-28 12:00:00")
	state := cachedState(t, at, screenpolicy.Rules{DailyQuotaSeconds: 600}, 100)
	state.apply(response{Action: "lock", Reason: "paused", PolicyDate: "2026-10-28", QuotaSeconds: 600, RemainingSeconds: 100}, at, at)
	if state.allowed(at) || state.CachedPolicy != nil || state.Reason != "paused" {
		t.Fatal("old cached schedule overruled a newer decision without a snapshot")
	}
}

func TestFirstCachedPolicyRetainsAcknowledgedUseFromNextDay(t *testing.T) {
	at := osloTime(t, "2026-10-28 23:59:59")
	state := cachedState(t, at, screenpolicy.Rules{DailyQuotaSeconds: 600}, 100)
	// Model an existing .2 client file with usage but no downloaded calendar.
	rules := state.CachedPolicy
	state.CachedPolicy = nil
	state.LocalDayUsage = nil
	state.account(11, at.Add(11*time.Second))
	report, err := state.prepareReport("pc", "child", "active", at.Add(11*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	state.apply(response{Policy: rules, Action: "allow", UsageAck: report.Usage.ID,
		PolicyDate: "2026-10-28", QuotaSeconds: 600, RemainingSeconds: 99,
		ServerTime: at, Timezone: "Europe/Oslo"}, at, at.Add(11*time.Second))
	if state.PendingUsage != nil || state.RemainingSeconds != 590 || state.LocalDayUsage["2026-10-29"] != 10 {
		t.Fatal("first snapshot lost the acknowledged part of next day's usage")
	}
}

func TestPolicyProtocolRejectsInvalidSnapshots(t *testing.T) {
	at := osloTime(t, "2026-10-28 12:00:00")
	for _, mutation := range []string{"valid", "quota", "date", "timezone", "duplicate weekday", "negative bonus", "contradictory pause"} {
		t.Run(mutation, func(t *testing.T) {
			rules := &screenpolicy.Rules{DailyQuotaSeconds: 600, BonusDate: "2026-10-28"}
			reply := response{Policy: rules, Action: "allow", Reason: "allowed", QuotaSeconds: 600, RemainingSeconds: 100,
				LeaseSeconds: 90, ServerTime: at, Timezone: "Europe/Oslo", PolicyDate: "2026-10-28"}
			switch mutation {
			case "quota":
				rules.DailyQuotaSeconds = 700
			case "date":
				rules.BonusDate = "2026-10-29"
			case "timezone":
				reply.Timezone = "invalid-zone"
			case "duplicate weekday":
				rules.Weekdays = []screenpolicy.Weekday{{Weekday: 1}, {Weekday: 1}}
			case "negative bonus":
				rules.BonusSeconds = -1
			case "contradictory pause":
				reply.Action, reply.Reason = "lock", "paused"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(reply)
			}))
			defer server.Close()
			_, err := postHeartbeat(context.Background(), newHTTPClient(), server.URL, "token", heartbeat{})
			if (err == nil) != (mutation == "valid") {
				t.Fatalf("unexpected snapshot validation result: %v", err)
			}
		})
	}
}

func TestOfflineScheduleHandlesMissingAndRepeatedDSTHours(t *testing.T) {
	// Explicit offsets distinguish the two occurrences of 02:30 in autumn.
	parse := func(value string) time.Time {
		at, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		location, _ := time.LoadLocation("Europe/Oslo")
		return at.In(location)
	}
	rules := screenpolicy.Rules{DailyQuotaSeconds: 600, Weekdays: []screenpolicy.Weekday{
		{Weekday: int(time.Sunday), Configured: true, QuotaSeconds: 600, StartMinute: 150, EndMinute: 165},
	}}
	at := parse("2026-10-25T02:44:59+02:00")
	state := cachedState(t, at, rules, 100)
	if !state.allowed(at) || state.allowed(at.Add(time.Second)) {
		t.Fatal("first occurrence of autumn window did not close")
	}
	repeated := parse("2026-10-25T02:30:00+01:00")
	if !state.allowed(repeated) || state.RemainingSeconds != 100 || state.allowed(repeated.Add(15*time.Minute)) {
		t.Fatal("repeated autumn window failed or refilled quota")
	}
	rules.Weekdays[0].EndMinute = 240 // 02:30-04:00; spring skips 02:30.
	at = parse("2026-03-29T01:59:59+01:00")
	state = cachedState(t, at, rules, 100)
	if state.allowed(at) || !state.allowed(at.Add(time.Second)) || state.allowed(parse("2026-03-29T04:00:00+02:00")) {
		t.Fatal("spring window did not follow actual local time")
	}
}

func TestLoopKeepsUsingCachedRulesAcrossOfflineMidnight(t *testing.T) {
	at := osloTime(t, "2026-10-28 23:59:58")
	state := cachedState(t, at, screenpolicy.Rules{DailyQuotaSeconds: 600, BonusSeconds: 300}, 20)
	h := startTestClient(t, state, at, "", true)
	h.request().reply <- loopReply{status: 503, body: `{}`}
	h.await(func(s clientState) bool { return s.ServerUnavailable && s.Report != nil })
	h.poll(time.Second)
	got := h.poll(time.Second)
	if got.PolicyDate != "2026-10-29" || got.QuotaSeconds != 600 || got.RemainingSeconds != 600 || h.locks.Load() != 0 {
		t.Fatalf("midnight in production loop lost cached rules: %+v", got)
	}
	got = h.poll(time.Second)
	if got.RemainingSeconds != 599 || got.unacknowledgedUsage("2026-10-28") != 2 || got.unacknowledgedUsage("2026-10-29") != 1 {
		t.Fatal("local counting and later reconciliation disagreed at midnight")
	}
}

func TestSavedPolicyValidationAndClockRollback(t *testing.T) {
	at := osloTime(t, "2026-10-28 12:00:00")
	state := cachedState(t, at, screenpolicy.Rules{DailyQuotaSeconds: 600}, 100)
	state.account(10, at.Add(10*time.Second))
	if state.allowed(at) || state.Reason != "clock_changed" || state.allowed(at.AddDate(0, 0, 1)) {
		t.Fatal("clock rollback was reopened by the cached schedule")
	}
	if err := validateSavedState(state, "client"); err != nil {
		t.Fatal(err)
	}
	state.LocalDayUsage["2026-10-28"] = -1
	if err := validateSavedState(state, "client"); err == nil {
		t.Fatal("invalid daily ledger accepted")
	}
	state.LocalDayUsage = nil
	state.CachedPolicy.Weekdays = []screenpolicy.Weekday{{Weekday: 7}}
	if err := validateSavedState(state, "client"); err == nil {
		t.Fatal("invalid cached schedule accepted")
	}
}
