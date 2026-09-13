package main

import (
	"testing"
	"time"
)

func TestRecordHeartbeatUsesServiceTimeAndAppliesQuota(t *testing.T) {
	app := testApplication(t)
	service := app.service
	now := time.Date(2026, time.September, 13, 12, 1, 0, 0, time.Local)
	service.now = func() time.Time { return now }
	if err := service.setUserQuota("barn1", 60); err != nil {
		t.Fatal(err)
	}
	if _, err := service.addHeartbeat(heartbeat{DeviceID: "pc-barn1", User: "barn1", ReportedAt: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}

	result, err := service.recordHeartbeat(heartbeat{DeviceID: "pc-barn1", User: "barn1", ReportedAt: now.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "lock" || result.DailyTotalSeconds != 60 || result.RemainingSeconds != 0 || !result.ReportedAt.Equal(now) {
		t.Fatalf("result = %#v", result)
	}
}
