package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMeasuredHeartbeatAccountingAndIdempotency(t *testing.T) {
	s := testApplication(t).service
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	record := func(id, state string, active, advance, want int) heartbeatResult {
		t.Helper()
		now = now.Add(time.Duration(advance) * time.Second)
		got, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: id, SessionState: state, ActiveSeconds: active})
		if err != nil {
			t.Fatal(err)
		}
		if got.DailyTotalSeconds != want {
			t.Fatalf("%s: total %d, want %d", id, got.DailyTotalSeconds, want)
		}
		return got
	}
	record("start", "active", 0, 0, 0)
	record("work", "active", 17, 30, 17)
	record("lock", "locked", 8, 10, 25)
	record("locked-poll", "locked", 0, 30, 25)
	record("unlock", "active", 0, 30, 25)
	record("active-again", "active", 30, 30, 55)
	record("active-again", "active", 30, 30, 55) // lost response retried
	record("after-retry", "active", 60, 30, 115)
	// Queued usage can consume unused elapsed time, but never more than the
	// total 195 seconds that have elapsed since the first server receipt.
	record("clamped", "active", 86400, 5, 195)
	var count int
	if err := s.repository.db.QueryRow(`SELECT COUNT(*) FROM heartbeats WHERE user = 'child'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 8 {
		t.Fatalf("rows = %d, want 8 unique reports", count)
	}
}

func TestMeasuredActivitySplitsMidnightInConfiguredTimezone(t *testing.T) {
	s := testApplication(t).service
	s.location = osloLocation(t)
	now := time.Date(2026, 9, 18, 23, 59, 40, 0, s.location)
	s.now = func() time.Time { return now }
	if _, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "a"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	got, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "b", ActiveSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	if got.DailyTotalSeconds != 10 || got.PolicyDate != "2026-09-19" {
		t.Fatalf("current day: %+v", got)
	}
	previous, err := s.dailyTotal("child", "2026-09-18")
	if err != nil {
		t.Fatal(err)
	}
	if previous != 20 {
		t.Fatalf("previous day = %d, want 20", previous)
	}
}

func TestMeasuredActivityAcrossDST(t *testing.T) {
	for _, start := range []time.Time{time.Date(2026, 3, 29, 0, 59, 45, 0, time.UTC), time.Date(2026, 10, 25, 0, 59, 45, 0, time.UTC)} {
		t.Run(start.Format("2006-01-02"), func(t *testing.T) {
			s := testApplication(t).service
			s.location = osloLocation(t)
			now := start
			s.now = func() time.Time { return now }
			if _, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "a"}); err != nil {
				t.Fatal(err)
			}
			now = now.Add(30 * time.Second)
			got, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "b", ActiveSeconds: 30})
			if err != nil {
				t.Fatal(err)
			}
			if got.DailyTotalSeconds != 30 {
				t.Fatalf("total = %d, want actual elapsed 30 seconds", got.DailyTotalSeconds)
			}
		})
	}
}

func TestBonusExpiresAndNeverBypassesPauseOrSchedule(t *testing.T) {
	s := testApplication(t).service
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	s.location = time.UTC
	s.now = func() time.Time { return now }
	if err := s.setUserQuota("child", 60); err != nil {
		t.Fatal(err)
	}
	if err := s.addBonusMinutes("child", 15); err != nil {
		t.Fatal(err)
	}
	policy, err := s.userPolicy("child")
	if err != nil {
		t.Fatal(err)
	}
	if got := evaluatePolicy(policy, 60, now); got.Action != "allow" || got.RemainingSeconds != 900 {
		t.Fatalf("bonus: %+v", got)
	}
	if err := s.setUserPaused("child", true); err != nil {
		t.Fatal(err)
	}
	policy, _ = s.userPolicy("child")
	if got := evaluatePolicy(policy, 60, now); got.Reason != "paused" {
		t.Fatalf("pause: %+v", got)
	}
	if err := s.setUserPaused("child", false); err != nil {
		t.Fatal(err)
	}
	if err := s.setWeekdayPolicies("child", []weekdayPolicy{{Weekday: 5, Configured: true, Disabled: true, EndMinute: 1440}}); err != nil {
		t.Fatal(err)
	}
	policy, _ = s.userPolicy("child")
	if got := evaluatePolicy(policy, 60, now); got.Reason != "schedule" {
		t.Fatalf("schedule: %+v", got)
	}
	now = nextLocalMidnight(now)
	policy, err = s.userPolicy("child")
	if err != nil {
		t.Fatal(err)
	}
	if policy.BonusSeconds != 0 {
		t.Fatalf("bonus persisted to next day: %+v", policy)
	}
}

func TestPolicyVersionsOnlyChangeForRealEdits(t *testing.T) {
	s := testApplication(t).service
	user := "child"
	steps := []struct {
		mutate  func() error
		version int
	}{
		{func() error { return s.setUserPaused(user, false) }, 0},
		{func() error { return s.setUserPaused(user, true) }, 1},
		{func() error { return s.setUserPaused(user, true) }, 1},
		{func() error { return s.setUserQuota(user, 3600) }, 2},
		{func() error { return s.setUserQuota(user, 3600) }, 2},
		{func() error {
			return s.setWeekdayPolicies(user, []weekdayPolicy{{Weekday: 6, Configured: true, QuotaSeconds: 7200, EndMinute: 1440}})
		}, 3},
		{func() error {
			return s.setWeekdayPolicies(user, []weekdayPolicy{{Weekday: 6, Configured: true, QuotaSeconds: 7200, EndMinute: 1440}})
		}, 3},
		{func() error { return s.addBonusMinutes(user, 15) }, 4},
		{func() error { return s.clearBonus(user) }, 5},
		{func() error { return s.clearBonus(user) }, 5},
		{func() error { return s.setWeekdayPolicies(user, nil) }, 6},
	}
	for index, step := range steps {
		if err := step.mutate(); err != nil {
			t.Fatalf("step %d: %v", index, err)
		}
		got, err := s.userPolicy(user)
		if err != nil {
			t.Fatal(err)
		}
		if got.PolicyVersion != step.version {
			t.Fatalf("step %d version %d, want %d", index, got.PolicyVersion, step.version)
		}
	}
}

func TestConcurrentMeasuredHeartbeatsAreIdempotent(t *testing.T) {
	s := testApplication(t).service
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if _, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "start"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	var wg sync.WaitGroup
	errors := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "same", ActiveSeconds: 30})
			if err != nil {
				errors <- err
			} else if got.DailyTotalSeconds != 30 {
				errors <- fmt.Errorf("total = %d, want 30", got.DailyTotalSeconds)
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestDailyHistoryAndIdleDevicePresence(t *testing.T) {
	s := testApplication(t).service
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	s.location = time.UTC
	s.now = func() time.Time { return now }
	if _, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "start", SessionState: "locked"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	if _, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "locked", SessionState: "locked"}); err != nil {
		t.Fatal(err)
	}
	items, err := s.todaysActivities()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].TotalSeconds != 0 || !items[0].Online || len(items[0].History) != 7 || len(items[0].Devices) != 1 || items[0].Devices[0].State != "locked" {
		t.Fatalf("activities = %+v", items)
	}
	if items[0].History[0].Date != "2026-09-12" || items[0].History[6].Date != "2026-09-18" {
		t.Fatalf("history = %+v", items[0].History)
	}
}
