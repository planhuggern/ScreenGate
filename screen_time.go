package main

import (
	"slices"
	"time"
)

type heartbeat struct {
	DeviceID      string    `json:"device_id"`
	User          string    `json:"user"`
	ActiveSeconds int       `json:"active_seconds"`
	ReportedAt    time.Time `json:"reported_at"`
}

type activity struct {
	User           string
	TotalSeconds   int
	LastReportedAt string
	QuotaSeconds   int
}

type recordedHeartbeat struct {
	deviceID   string
	reportedAt time.Time
}

const (
	heartbeatInterval = 30 * time.Second
	maxHeartbeatGap   = heartbeatInterval * 21 / 10
)

func screenTimeDecision(dailyTotal, quota int) (string, int) {
	if quota <= 0 {
		return "allow", 0
	}
	remaining := quota - dailyTotal
	if remaining <= 0 {
		return "lock", 0
	}
	return "allow", remaining
}

func calculateDailyTotal(heartbeats []recordedHeartbeat, date string) (int, error) {
	dayStart, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		return 0, err
	}
	dayEnd := dayStart.AddDate(0, 0, 1)

	byDevice := make(map[string][]time.Time)
	for _, heartbeat := range heartbeats {
		byDevice[heartbeat.deviceID] = append(byDevice[heartbeat.deviceID], heartbeat.reportedAt)
	}

	total := time.Duration(0)
	for _, timestamps := range byDevice {
		slices.SortFunc(timestamps, func(a, b time.Time) int { return a.Compare(b) })
		for i := 1; i < len(timestamps); i++ {
			start, end := timestamps[i-1], timestamps[i]
			if gap := end.Sub(start); gap <= 0 || gap > maxHeartbeatGap {
				continue
			}
			if start.Before(dayStart) {
				start = dayStart
			}
			if end.After(dayEnd) {
				end = dayEnd
			}
			if end.After(start) {
				total += end.Sub(start)
			}
		}
	}
	return int(total / time.Second), nil
}
