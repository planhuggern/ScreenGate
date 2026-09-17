package main

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func osloLocation(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func TestPolicyDecisions(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC) // Monday
	base := userPolicy{DailyQuotaSeconds: 3600}
	tests := []struct {
		name             string
		policy           userPolicy
		total            int
		at               time.Time
		action, reason   string
		remaining, lease int
	}{
		{"default quota", base, 3500, now, "allow", "allowed", 100, 90},
		{"last seconds", base, 3592, now, "allow", "allowed", 8, 8},
		{"exhausted", base, 3600, now, "lock", "quota", 0, 0},
		{"unlimited", userPolicy{}, 86400, now, "allow", "allowed", 0, 90},
		{"pause unlimited", userPolicy{Paused: true}, 0, now, "lock", "paused", 0, 0},
		{"bonus", userPolicy{DailyQuotaSeconds: 3600, BonusSeconds: 900}, 3600, now, "allow", "allowed", 900, 90},
		{"midnight lease", base, 0, now.Add(11*time.Hour + 59*time.Minute + 45*time.Second), "allow", "allowed", 3600, 15},
		{"weekday override", userPolicy{DailyQuotaSeconds: 3600, Weekdays: []weekdayPolicy{{Weekday: 1, Configured: true, QuotaSeconds: 7200, EndMinute: 1440}}}, 3600, now, "allow", "allowed", 3600, 90},
		{"inherited day", userPolicy{DailyQuotaSeconds: 3600, Weekdays: []weekdayPolicy{{Weekday: 1, Configured: false, QuotaSeconds: 7200}}}, 3500, now, "allow", "allowed", 100, 90},
		{"disabled day", userPolicy{Weekdays: []weekdayPolicy{{Weekday: 1, Configured: true, Disabled: true, EndMinute: 1440}}}, 0, now, "lock", "schedule", 0, 0},
		{"outside window", userPolicy{Weekdays: []weekdayPolicy{{Weekday: 1, Configured: true, StartMinute: 13 * 60, EndMinute: 20 * 60}}}, 0, now, "lock", "schedule", 0, 0},
		{"closing window", userPolicy{Weekdays: []weekdayPolicy{{Weekday: 1, Configured: true, StartMinute: 8 * 60, EndMinute: 12*60 + 1}}}, 0, now.Add(45 * time.Second), "allow", "allowed", 0, 15},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := evaluatePolicy(test.policy, test.total, test.at)
			if got.Action != test.action || got.Reason != test.reason || got.RemainingSeconds != test.remaining || got.LeaseSeconds != test.lease {
				t.Fatalf("decision = %+v; want %s/%s, remaining %d, lease %d", got, test.action, test.reason, test.remaining, test.lease)
			}
		})
	}
}

func TestOvernightWindowUsesPreviousDayAndCurrentQuota(t *testing.T) {
	p := userPolicy{DailyQuotaSeconds: 3600, Weekdays: []weekdayPolicy{
		{Weekday: 5, Configured: true, StartMinute: 22 * 60, EndMinute: 2 * 60, QuotaSeconds: 7200},
		{Weekday: 6, Configured: true, StartMinute: 10 * 60, EndMinute: 20 * 60, QuotaSeconds: 1800},
	}}
	friday := time.Date(2026, 9, 18, 23, 0, 0, 0, time.UTC)
	if got := evaluatePolicy(p, 3600, friday); got.Action != "allow" || got.QuotaSeconds != 7200 {
		t.Fatalf("Friday: %+v", got)
	}
	saturday := friday.Add(2 * time.Hour)
	if got := evaluatePolicy(p, 1000, saturday); got.Action != "allow" || got.QuotaSeconds != 1800 {
		t.Fatalf("Saturday overnight: %+v", got)
	}
	if got := evaluatePolicy(p, 1000, saturday.Add(time.Hour)); got.Action != "lock" || !got.NextAllowedAt.Equal(saturday.Add(9*time.Hour)) {
		t.Fatalf("Saturday closing: %+v", got)
	}
	p.Weekdays[1].Disabled = true
	if p.scheduleAllows(saturday) {
		t.Fatal("disabled calendar day must block previous day's overnight tail")
	}
}

func TestScheduleTransitionsAcrossDST(t *testing.T) {
	location := osloLocation(t)
	p := userPolicy{Weekdays: []weekdayPolicy{{Weekday: 0, Configured: true, StartMinute: 2 * 60, EndMinute: 4 * 60}}}
	beforeSpring := time.Date(2026, 3, 29, 0, 59, 50, 0, time.UTC).In(location) // 01:59:50 CET
	got := evaluatePolicy(p, 0, beforeSpring)
	want := beforeSpring.Add(10 * time.Second) // 03:00 CEST; 02:00 did not exist
	if got.Action != "lock" || !got.NextAllowedAt.Equal(want) {
		t.Fatalf("spring transition: %+v, want next %s", got, want)
	}
	firstAutumn := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC).In(location)
	secondAutumn := firstAutumn.Add(time.Hour)
	if !p.scheduleAllows(firstAutumn) || !p.scheduleAllows(secondAutumn) {
		t.Fatal("both occurrences of 02:30 must use the same local window")
	}
	if nextLocalMidnight(time.Date(2026, 3, 29, 0, 0, 0, 0, location)).Sub(time.Date(2026, 3, 29, 0, 0, 0, 0, location)) != 23*time.Hour {
		t.Fatal("spring day must be 23 hours")
	}
	if nextLocalMidnight(time.Date(2026, 10, 25, 0, 0, 0, 0, location)).Sub(time.Date(2026, 10, 25, 0, 0, 0, 0, location)) != 25*time.Hour {
		t.Fatal("autumn day must be 25 hours")
	}
}

func TestAllDaysDisabledHaveNoNextAllowedTime(t *testing.T) {
	p := userPolicy{}
	for day := 0; day < 7; day++ {
		p.Weekdays = append(p.Weekdays, weekdayPolicy{Weekday: day, Configured: true, Disabled: true, EndMinute: 1440})
	}
	got := evaluatePolicy(p, 0, time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	if got.Action != "lock" || !got.NextAllowedAt.IsZero() {
		t.Fatalf("decision = %+v", got)
	}
}

func TestValidateWeekdayPolicies(t *testing.T) {
	for _, day := range []weekdayPolicy{
		{Weekday: -1}, {Weekday: 7}, {Configured: true, QuotaSeconds: -1, EndMinute: 1440},
		{Configured: true, QuotaSeconds: 86401, EndMinute: 1440}, {Configured: true, StartMinute: 1440, EndMinute: 1440},
		{Configured: true, StartMinute: 600, EndMinute: 600}, {Configured: true, EndMinute: 1441},
	} {
		if err := validateWeekdayPolicies([]weekdayPolicy{day}); err == nil {
			t.Fatalf("accepted %+v", day)
		}
	}
	if err := validateWeekdayPolicies([]weekdayPolicy{{Weekday: 1}, {Weekday: 1}}); err == nil {
		t.Fatal("accepted duplicate weekdays")
	}
}

func TestPlannedLockWarningsDistinguishBedtimeFromQuotaReset(t *testing.T) {
	now := time.Date(2026, 9, 18, 19, 50, 0, 0, time.UTC)
	p := userPolicy{DailyQuotaSeconds: 3600, Weekdays: []weekdayPolicy{{Weekday: 5, Configured: true, StartMinute: 8 * 60, EndMinute: 20 * 60}}}
	d := evaluatePolicy(p, 0, now)
	if !d.NextLockAt.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("bedtime lock=%s", d.NextLockAt)
	}
	d = evaluatePolicy(userPolicy{DailyQuotaSeconds: 3600}, 0, now)
	if !d.NextLockAt.IsZero() {
		t.Fatal("normal midnight reset should not warn about locking")
	}
	p = userPolicy{Weekdays: []weekdayPolicy{{Weekday: 6, Configured: true, Disabled: true, EndMinute: 1440}}}
	d = evaluatePolicy(p, 0, now)
	if !d.NextLockAt.Equal(nextLocalMidnight(now)) {
		t.Fatal("tomorrow's disabled day should warn at midnight")
	}
}
