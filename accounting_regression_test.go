package main

import (
	"testing"
	"time"
)

func TestOfflineUsageIsNotChargedAfterLongShutdown(t *testing.T) {
	s := testApplication(t).service
	s.location = osloLocation(t)
	now := time.Date(2026, 9, 18, 23, 58, 0, 0, s.location)
	s.now = func() time.Time { return now }
	if _, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "first", SessionState: "active"}); err != nil {
		t.Fatal(err)
	}
	// A stale client report cannot charge time across a long heartbeat gap.
	now = now.AddDate(0, 0, 7)
	result, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "offline", ActivityDate: "2026-09-18", ActiveSeconds: 90, SessionState: "locked"})
	if err != nil {
		t.Fatal(err)
	}
	if result.DailyTotalSeconds != 0 {
		t.Fatalf("today was charged %d seconds", result.DailyTotalSeconds)
	}
	previous, err := s.dailyTotal("child", "2026-09-18")
	if err != nil || previous != 0 {
		t.Fatalf("original day=%d err=%v", previous, err)
	}
	if _, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "offline", ActivityDate: "2026-09-18", ActiveSeconds: 90}); err != nil {
		t.Fatal(err)
	}
	previous, _ = s.dailyTotal("child", "2026-09-18")
	if previous != 0 {
		t.Fatal("retry doubled past usage")
	}
}

func TestQueuedReportsCannotExceedServerElapsedTime(t *testing.T) {
	s := testApplication(t).service
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	_, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(35 * time.Second)
	_, err = s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "report", ActiveSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	// Five more seconds accrued while the first report was in transit.
	result, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "queued", ActiveSeconds: 5})
	if err != nil || result.DailyTotalSeconds != 35 {
		t.Fatalf("total=%d err=%v", result.DailyTotalSeconds, err)
	}
	result, err = s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "impossible", ActiveSeconds: 100})
	if err != nil || result.DailyTotalSeconds != 35 {
		t.Fatalf("impossible extra credit: %d err=%v", result.DailyTotalSeconds, err)
	}
}

func TestLegacyActivityDateIsIgnored(t *testing.T) {
	s := testApplication(t).service
	s.location = time.UTC
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	_, _ = s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "first"})
	now = now.Add(30 * time.Second)
	for _, date := range []string{"2026-09-19", "not-a-date", "2026-09-17"} {
		_, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", HeartbeatID: "bad-" + date, ActiveSeconds: 30, ActivityDate: date})
		if err != nil {
			t.Fatalf("date=%q err=%v", date, err)
		}
	}
}

func TestUnlockedIdleHeartbeatCountsServerTime(t *testing.T) {
	s := testApplication(t).service
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if _, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", SessionState: "idle"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	got, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", SessionState: "idle"})
	if err != nil || got.DailyTotalSeconds != 30 {
		t.Fatalf("total=%d err=%v", got.DailyTotalSeconds, err)
	}
}

func TestLockedHeartbeatsStopQuotaUntilUnlock(t *testing.T) {
	s := testApplication(t).service
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	for _, step := range []struct {
		after time.Duration
		state string
		want  int
	}{
		{0, "locked", 0},
		{30 * time.Second, "locked", 0},
		{time.Second, "active", 0},
		{30 * time.Second, "active", 30},
		{time.Second, "locked", 31},
		{30 * time.Second, "locked", 31},
		{30 * time.Second, "locked", 31},
		{30 * time.Second, "locked", 31},
		{time.Second, "active", 31},
		{30 * time.Second, "active", 61},
	} {
		now = now.Add(step.after)
		result, err := s.recordHeartbeat(heartbeat{User: "child", DeviceID: "pc", SessionState: step.state})
		if err != nil {
			t.Fatal(err)
		}
		if result.DailyTotalSeconds != step.want {
			t.Fatalf("at %s state=%s total=%d, want %d", now, step.state, result.DailyTotalSeconds, step.want)
		}
	}
}
