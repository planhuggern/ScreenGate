package main

import (
	"time"

	"screengate/internal/screenpolicy"
)

func (s *clientState) policyLocation() *time.Location {
	if s.PolicyTimezone != "" && s.PolicyTimezone != "Local" {
		if location, err := time.LoadLocation(s.PolicyTimezone); err == nil {
			return location
		}
	}
	return time.FixedZone("server", s.PolicyUTCOffset)
}

// Refresh calendar rules without refilling today's authoritative rest quota.
// Only moving to a later date grants that day's downloaded quota.
func (s *clientState) refreshPolicy(now time.Time) {
	if s.CachedPolicy == nil || !s.ServerContacted || s.LocalBlockReason != "" {
		return
	}
	at := now.Add(s.ServerClockOffset).In(s.policyLocation())
	date := at.Format("2006-01-02")
	if date < s.PolicyDate {
		s.invalidate("clock_changed")
		return
	}
	if date != s.PolicyDate {
		s.PolicyDate = date
		s.QuotaSeconds = s.CachedPolicy.QuotaAt(at)
		s.RemainingSeconds = max(0, s.QuotaSeconds-s.LocalDayUsage[date])
		s.NextScheduleCheck = time.Time{}
	}
	allows := s.CachedPolicy.Allows(at)
	s.Action, s.Reason = "allow", "allowed"
	switch {
	case s.CachedPolicy.Paused:
		s.Action, s.Reason = "lock", "paused"
	case !allows:
		s.Action, s.Reason = "lock", "schedule"
	case s.QuotaSeconds > 0 && s.RemainingSeconds == 0:
		s.Action, s.Reason = "lock", "quota"
	}
	if s.NextScheduleCheck.IsZero() || !at.Before(s.NextScheduleCheck) {
		// Cache the search until the next boundary, rather than walking the
		// weekly schedule on every one-second poll.
		s.NextScheduleCheck = screenpolicy.MidnightAfter(at)
		s.ScheduledLockAt = time.Time{}
		if change := s.CachedPolicy.NextState(at, !allows); !change.IsZero() {
			if change.Before(s.NextScheduleCheck) {
				s.NextScheduleCheck = change
			}
			if allows {
				s.ScheduledLockAt = change.Add(-s.ServerClockOffset)
			}
		}
	}
	// Older days cannot be revisited without a new server decision. Retain
	// yesterday as well as today to cover a response in flight at midnight.
	cutoff := at.AddDate(0, 0, -1).Format("2006-01-02")
	for day := range s.LocalDayUsage {
		if day < cutoff {
			delete(s.LocalDayUsage, day)
		}
	}
}

// Split measured use at actual calendar midnights, including 23/25-hour days.
// This ledger also retains acknowledged use, so a delayed pre-midnight reply
// cannot grant tomorrow's quota again after some of it has already been used.
func (s *clientState) rememberDailyUsage(interval usageInterval) {
	if s.LocalDayUsage == nil {
		s.LocalDayUsage = make(map[string]int)
	}
	for from := interval.Start.In(s.policyLocation()); from.Before(interval.End); {
		until := screenpolicy.MidnightAfter(from)
		if interval.End.Before(until) {
			until = interval.End
		}
		date := from.Format("2006-01-02")
		s.LocalDayUsage[date] += int(until.Sub(from) / time.Second)
		from = until.In(s.policyLocation())
	}
}

func (s *clientState) usageOnDate(interval usageInterval, date string) int {
	start, err := time.ParseInLocation("2006-01-02", date, s.policyLocation())
	if err != nil {
		return 0
	}
	end := start.AddDate(0, 0, 1)
	from, until := interval.Start, interval.End
	if from.Before(start) {
		from = start
	}
	if until.After(end) {
		until = end
	}
	return max(0, int(until.Sub(from)/time.Second))
}

func (s *clientState) installPolicy(result response) {
	// An older server supplies only a decision. Do not let a previously
	// cached schedule overrule that newer decision (for example a pause).
	if result.Policy == nil {
		s.CachedPolicy = nil
		s.LocalDayUsage = nil
		return
	}
	if s.CachedPolicy == nil {
		s.LocalDayUsage = nil
		if s.PendingUsage != nil {
			for _, interval := range s.PendingUsage.Intervals {
				s.rememberDailyUsage(interval)
			}
		}
		for _, interval := range s.UnreportedUsage {
			s.rememberDailyUsage(interval)
		}
	}
	s.CachedPolicy = result.Policy
	s.NextScheduleCheck = time.Time{}
}
