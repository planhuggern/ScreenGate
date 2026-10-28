package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// A batch is immutable until acknowledged. Its identity is independent of the
// heartbeat identity, so reporting a lock never discards unacknowledged usage.
type usageBatch struct {
	ID               string          `json:"id"`
	RemainingSeconds int             `json:"remaining_seconds"`
	QuotaSeconds     int             `json:"quota_seconds"`
	PolicyDate       string          `json:"policy_date"`
	Legacy           bool            `json:"legacy,omitempty"`
	Intervals        []usageInterval `json:"intervals"`
}

type usageInterval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func validateUsage(h heartbeat) error {
	if h.Usage == nil {
		return nil
	}
	b := h.Usage
	if h.HeartbeatID == "" || !validIdentity(b.ID) || len(b.Intervals) > 32 || b.RemainingSeconds < 0 || b.QuotaSeconds < 0 || b.QuotaSeconds > 172800 {
		return errInvalidHeartbeat
	}
	if b.Legacy {
		date, err := time.Parse("2006-01-02", b.PolicyDate)
		if err != nil || date.Year() < 2000 || len(b.Intervals) != 0 {
			return errInvalidHeartbeat
		}
		location := h.accountingLocation
		if location == nil {
			location = time.Local
		}
		if b.PolicyDate > h.ReportedAt.In(location).Format("2006-01-02") {
			return errInvalidHeartbeat
		}
	}
	var previous time.Time
	for _, interval := range b.Intervals {
		duration := interval.End.Sub(interval.Start)
		if interval.Start.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || duration <= 0 || duration > time.Hour || interval.End.After(h.ReportedAt.Add(5*time.Minute)) || (!previous.IsZero() && interval.Start.Before(previous)) {
			return errInvalidHeartbeat
		}
		previous = interval.End
	}
	return nil
}

// Called inside the same transaction that records the heartbeat and presence.
// New clients are charged from these intervals, never also from receipt gaps.
func recordUsageBatch(tx *sql.Tx, h heartbeat) (int64, error) {
	b := h.Usage
	hash, err := usageBatchHash(b)
	if err != nil {
		return 0, err
	}
	var existing string
	err = tx.QueryRow(`SELECT payload_hash FROM usage_batches WHERE user=? AND device_id=? AND batch_id=?`, h.User, h.DeviceID, b.ID).Scan(&existing)
	if err == nil {
		if existing != hash {
			return 0, errInvalidHeartbeat
		}
		return 0, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO usage_batches(user,device_id,batch_id,payload_hash) VALUES(?,?,?,?)`, h.User, h.DeviceID, b.ID, hash); err != nil {
		return 0, err
	}
	var credit int64
	if b.Legacy && b.QuotaSeconds > 0 {
		// Old state has no timestamps for offline use. Establish a conservative
		// floor from its saved quota; never add the same historical total twice.
		location := h.accountingLocation
		if location == nil {
			location = time.Local
		}
		start, _ := time.ParseInLocation("2006-01-02", b.PolicyDate, location)
		end := start.AddDate(0, 0, 1)
		rows, err := tx.Query(usageHistoryRangeQuery, h.User, start.Add(-maxHeartbeatGap).Unix(), end.Add(maxReportedActivity).Unix(), h.User, start.Add(-maxHeartbeatGap).Unix(), end.Add(maxReportedActivity).Unix())
		if err != nil {
			return 0, err
		}
		history, err := scanHeartbeats(rows)
		if err != nil {
			return 0, err
		}
		total, err := calculateDailyTotalInLocation(history, b.PolicyDate, location)
		if err != nil {
			return 0, err
		}
		floor := min(max(0, b.QuotaSeconds-b.RemainingSeconds), int(end.Sub(start)/time.Second))
		if extra := floor - total; extra > 0 {
			until := end
			if h.ReportedAt.Before(until) {
				until = h.ReportedAt
			}
			from := until.Add(-time.Duration(extra) * time.Second)
			if from.Before(start) {
				from = start
			}
			amount, err := insertUsageInterval(tx, h, usageInterval{Start: from, End: until})
			if err != nil {
				return 0, err
			}
			credit += amount
		}
	}
	for _, interval := range b.Intervals {
		amount, err := insertUsageInterval(tx, h, interval)
		if err != nil {
			return 0, err
		}
		credit += amount
	}
	return credit, nil
}

func usageBatchHash(batch *usageBatch) (string, error) {
	data, err := json.Marshal(batch)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func insertUsageInterval(tx *sql.Tx, h heartbeat, interval usageInterval) (int64, error) {
	credit := interval.End.Sub(interval.Start).Milliseconds()
	if credit <= 0 {
		return 0, nil
	}
	_, err := tx.Exec(`INSERT INTO usage_intervals(user,device_id,received_at,received_unix,start_unix,end_at,credited_milliseconds) VALUES(?,?,?,?,?,?,?)`, h.User, h.DeviceID, h.ReportedAt.UTC().Format(time.RFC3339Nano), h.ReportedAt.Unix(), interval.Start.Unix(), interval.End.UTC().Format(time.RFC3339Nano), credit)
	return credit, err
}

const usageHistoryQuery = `SELECT device_id,reported_at,credited_milliseconds,credited_until,session_state FROM (
 SELECT id,reported_unix,device_id,reported_at,credited_milliseconds,credited_until,session_state FROM heartbeats WHERE user=?
 UNION ALL
 SELECT id,received_unix,device_id,received_at,credited_milliseconds,end_at,'locked' FROM usage_intervals WHERE user=?
) ORDER BY reported_unix,id`

const usageHistoryRangeQuery = `SELECT device_id,reported_at,credited_milliseconds,credited_until,session_state FROM (
 SELECT id,reported_unix,device_id,reported_at,credited_milliseconds,credited_until,session_state FROM heartbeats WHERE user=? AND accounting_unix>=? AND accounting_unix<=?
 UNION ALL
 SELECT id,received_unix,device_id,received_at,credited_milliseconds,end_at,'locked' FROM usage_intervals WHERE user=? AND start_unix>=? AND start_unix<=?
) ORDER BY reported_unix,id`
