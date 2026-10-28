package main

import (
	"crypto/rand"
	"encoding/hex"
	"time"
	_ "time/tzdata"
)

// Keep a batch unchanged until the server acknowledges its ID, even if the
// heartbeat ID changes because the Windows session was locked or unlocked.
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

func newReportID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

func (s *clientState) prepareUsage() error {
	if s.PendingUsage != nil {
		return nil
	}
	id, err := newReportID()
	if err != nil {
		return err
	}
	if !s.UsageStarted {
		// An old pending heartbeat has no new usage acknowledgement contract.
		// Its saved rest quota is reconciled once through the legacy baseline.
		s.Report = nil
	}
	count := min(32, len(s.UnreportedUsage))
	s.PendingUsage = &usageBatch{
		ID: id, RemainingSeconds: s.RemainingSeconds, QuotaSeconds: s.QuotaSeconds,
		PolicyDate: s.PolicyDate, Legacy: !s.UsageStarted && s.ServerContacted && s.PolicyDate != "" && s.QuotaSeconds > 0,
		Intervals: append([]usageInterval(nil), s.UnreportedUsage[:count]...),
	}
	s.UnreportedUsage = append([]usageInterval(nil), s.UnreportedUsage[count:]...)
	s.UsageStarted = true
	return nil
}

func (s *clientState) recordUsage(seconds int, now time.Time) usageInterval {
	if seconds <= 0 {
		return usageInterval{}
	}
	end := time.Unix(now.Add(s.ServerClockOffset).Unix(), 0).UTC()
	start := end.Add(-time.Duration(seconds) * time.Second)
	if start.Before(s.LastUsageEnd) {
		start = s.LastUsageEnd
		end = start.Add(time.Duration(seconds) * time.Second)
	}
	measured := usageInterval{Start: start, End: end}
	if !s.UsageStarted {
		return measured
	}
	s.LastUsageEnd = end
	for start.Before(end) {
		until := start.Truncate(time.Hour).Add(time.Hour)
		if end.Before(until) {
			until = end
		}
		last := len(s.UnreportedUsage) - 1
		if last >= 0 && s.UnreportedUsage[last].End.Equal(start) && until.Sub(s.UnreportedUsage[last].Start) <= time.Hour {
			s.UnreportedUsage[last].End = until
		} else {
			s.UnreportedUsage = append(s.UnreportedUsage, usageInterval{Start: start, End: until})
		}
		start = until
	}
	return measured
}

func (s *clientState) updateUsageClock(result response, sentAt time.Time) {
	if !result.ServerTime.IsZero() {
		offset := result.ServerTime.Sub(sentAt)
		if !s.UsageClockKnown {
			// Usage measured while the first request waited was still in the
			// PC's clock. Calibrate it before sending it to the server.
			shift := offset - s.ServerClockOffset
			for i := range s.UnreportedUsage {
				s.UnreportedUsage[i].Start = s.UnreportedUsage[i].Start.Add(shift)
				s.UnreportedUsage[i].End = s.UnreportedUsage[i].End.Add(shift)
			}
			if !s.LastUsageEnd.IsZero() {
				s.LastUsageEnd = s.LastUsageEnd.Add(shift)
			}
		}
		s.ServerClockOffset = offset
		s.UsageClockKnown = true
		_, s.PolicyUTCOffset = result.ServerTime.Zone()
	}
	if result.Timezone != "" {
		s.PolicyTimezone = result.Timezone
	}
}

func (s *clientState) unacknowledgedUsage(date string) int {
	location := s.policyLocation()
	start, err := time.ParseInLocation("2006-01-02", date, location)
	if err != nil {
		return s.PendingSeconds // Legacy/direct callers without a policy date.
	}
	end := start.AddDate(0, 0, 1)
	intervals := s.UnreportedUsage
	if s.PendingUsage != nil {
		intervals = append(append([]usageInterval(nil), intervals...), s.PendingUsage.Intervals...)
	}
	var total time.Duration
	for _, interval := range intervals {
		from, until := interval.Start, interval.End
		if from.Before(start) {
			from = start
		}
		if until.After(end) {
			until = end
		}
		if until.After(from) {
			total += until.Sub(from)
		}
	}
	return int(total / time.Second)
}
