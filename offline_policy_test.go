package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestHeartbeatPublishesCalendarRulesAlongsideCurrentDecision(t *testing.T) {
	a := securedApplication(t)
	d := enrolledDevice(t, a, "child", "pc")
	location, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 19, 0, 0, 0, location)
	a.service.now = func() time.Time { return now }
	a.service.location = location
	if err := a.service.setUserQuota(d.User, 1800); err != nil {
		t.Fatal(err)
	}
	if err := a.service.setWeekdayPolicies(d.User, []weekdayPolicy{
		{Weekday: int(time.Wednesday), Configured: true, QuotaSeconds: 600, StartMinute: 18 * 60, EndMinute: 20 * 60},
		{Weekday: int(time.Thursday), Configured: true, Disabled: true, EndMinute: 1440},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.service.addBonusMinutes(d.User, 5); err != nil {
		t.Fatal(err)
	}
	for _, paused := range []bool{false, true} {
		if err := a.service.setUserPaused(d.User, paused); err != nil {
			t.Fatal(err)
		}
		h := measuredHeartbeat(d.User, d.DeviceID, "report", "batch", now.Add(-time.Minute), now)
		body, _ := json.Marshal(h)
		w := serve(a, deviceRequest(d.Token, string(body)))
		var got response
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
			t.Fatalf("heartbeat failed: %d %v %s", w.Code, err, w.Body.String())
		}
		if got.Policy == nil || got.Policy.Validate() != nil || got.Policy.Paused != paused || len(got.Policy.Weekdays) != 7 || got.Timezone != "Europe/Oslo" {
			t.Fatalf("missing or invalid cached policy: %+v", got)
		}
		if got.Policy.DailyQuotaSeconds != 1800 || got.QuotaSeconds != 900 || got.RemainingSeconds != 840 || got.Policy.QuotaAt(now) != 900 || got.Policy.BonusDate != "2026-10-28" {
			t.Fatalf("snapshot and current quota disagree: %+v", got)
		}
		thursday := now.AddDate(0, 0, 1)
		friday := now.AddDate(0, 0, 2)
		if got.Policy.Allows(thursday) || !got.Policy.Allows(friday) || got.Policy.QuotaAt(friday) != 1800 {
			t.Fatal("snapshot lost disabled day, inherited quota or bonus expiry")
		}
		if (got.Action == "lock") != paused || got.PolicyVersion == 0 {
			t.Fatal("server decision lost current pause or version")
		}
	}
}
