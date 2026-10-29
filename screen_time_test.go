package main

import (
	"testing"
	"time"
)

func TestCalculateDailyTotal(t *testing.T) {
	day := "2026-09-09"
	start := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.Local)
	heartbeats := []recordedHeartbeat{
		{deviceID: "pc-barn1", reportedAt: start},
		{deviceID: "pc-barn1", reportedAt: start.Add(63 * time.Second)},
		{deviceID: "pc-barn1", reportedAt: start.Add(127 * time.Second)},
		{deviceID: "pc-barn2", reportedAt: start},
		{deviceID: "pc-barn2", reportedAt: start.Add(30 * time.Second)},
	}

	total, err := calculateDailyTotalInLocation(heartbeats, day, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if total != 93 {
		t.Fatalf("daily total = %d, want 93", total)
	}
}

func TestCalculateDailyTotalSplitsAtMidnight(t *testing.T) {
	heartbeats := []recordedHeartbeat{
		{deviceID: "pc-barn1", reportedAt: time.Date(2026, time.September, 8, 23, 59, 40, 0, time.Local)},
		{deviceID: "pc-barn1", reportedAt: time.Date(2026, time.September, 9, 0, 0, 10, 0, time.Local)},
	}

	total, err := calculateDailyTotalInLocation(heartbeats, "2026-09-09", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if total != 10 {
		t.Fatalf("daily total = %d, want 10", total)
	}
}
