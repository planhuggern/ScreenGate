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
	HeartbeatID   string    `json:"heartbeat_id,omitempty"`
	SessionState  string    `json:"session_state,omitempty"`
	ActivityDate  string    `json:"activity_date,omitempty"`
}

type activity struct {
	User             string
	TotalSeconds     int
	LastReportedAt   string
	QuotaSeconds     int
	BaseQuotaSeconds int
	BonusSeconds     int
	RemainingSeconds int
	Paused           bool
	Unlimited        bool
	Action           string
	Reason           string
	NextAllowedAt    time.Time
	Online           bool
	Devices          []deviceActivity
	History          []dailyUsage
	Weekdays         []weekdayPolicy
}

type deviceActivity struct {
	ID             string
	LastReportedAt string
	Online         bool
	State          string
}

type dailyUsage struct {
	Date         string
	TotalSeconds int
}

type recordedHeartbeat struct {
	deviceID             string
	reportedAt           time.Time
	creditedMilliseconds *int64
	creditedUntil        time.Time
	sessionState         string
}

const (
	heartbeatInterval   = 30 * time.Second
	maxHeartbeatGap     = heartbeatInterval * 21 / 10
	maxReportedActivity = 24 * time.Hour
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
	return calculateDailyTotalInLocation(heartbeats, date, time.Local)
}

func calculateDailyTotalInLocation(heartbeats []recordedHeartbeat, date string, location *time.Location) (int, error) {
	dayStart, err := time.ParseInLocation("2006-01-02", date, location)
	if err != nil {
		return 0, err
	}
	dayEnd := dayStart.AddDate(0, 0, 1)

	byDevice := make(map[string][]recordedHeartbeat)
	for _, heartbeat := range heartbeats {
		byDevice[heartbeat.deviceID] = append(byDevice[heartbeat.deviceID], heartbeat)
	}

	total := time.Duration(0)
	for _, samples := range byDevice {
		slices.SortStableFunc(samples, func(a, b recordedHeartbeat) int { return a.reportedAt.Compare(b.reportedAt) })
		for i, sample := range samples {
			end := sample.reportedAt
			var start time.Time
			if sample.creditedMilliseconds != nil {
				if !sample.creditedUntil.IsZero() {
					end = sample.creditedUntil
				}
				start = end.Add(-time.Duration(*sample.creditedMilliseconds) * time.Millisecond)
			} else {
				if i == 0 || (samples[i-1].sessionState != "" && samples[i-1].sessionState != "active") {
					continue
				}
				start = samples[i-1].reportedAt
				if gap := end.Sub(start); gap <= 0 || gap > maxHeartbeatGap {
					continue
				}
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
