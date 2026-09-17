package main

import (
	"errors"
	"strings"
	"time"
)

var errInvalidPolicy = errors.New("invalid screen-time policy")

// Weekdays use time.Weekday numbering: Sunday is 0. An unconfigured day
// inherits the default quota and allows all day. Zero quota means unlimited;
// Disabled explicitly prohibits screen time for the entire calendar day.
type weekdayPolicy struct {
	Weekday      int
	Configured   bool
	QuotaSeconds int
	Disabled     bool
	StartMinute  int
	EndMinute    int
}

type userPolicy struct {
	User              string
	DailyQuotaSeconds int
	Paused            bool
	Weekdays          []weekdayPolicy
	BonusSeconds      int
	PolicyVersion     int
}

type policyDecision struct {
	Action           string
	Reason           string
	QuotaSeconds     int
	RemainingSeconds int
	Unlimited        bool
	NextAllowedAt    time.Time
	NextTransitionAt time.Time
	LeaseSeconds     int
}

func validPolicyUser(user string) bool {
	return strings.TrimSpace(user) != "" && len(user) <= 200
}

func validateWeekdayPolicies(days []weekdayPolicy) error {
	seen := [7]bool{}
	for _, day := range days {
		if day.Weekday < 0 || day.Weekday > 6 || seen[day.Weekday] {
			return errInvalidPolicy
		}
		seen[day.Weekday] = true
		if !day.Configured {
			continue
		}
		if day.QuotaSeconds < 0 || day.QuotaSeconds > 86400 || day.StartMinute < 0 || day.StartMinute > 1439 || day.EndMinute < 0 || day.EndMinute > 1440 || day.StartMinute == day.EndMinute {
			return errInvalidPolicy
		}
	}
	return nil
}

func (p userPolicy) day(weekday time.Weekday) weekdayPolicy {
	for _, day := range p.Weekdays {
		if day.Weekday == int(weekday) && day.Configured {
			return day
		}
	}
	return weekdayPolicy{Weekday: int(weekday), QuotaSeconds: p.DailyQuotaSeconds, EndMinute: 1440}
}

func (p userPolicy) scheduleAllows(at time.Time) bool {
	day := p.day(at.Weekday())
	if day.Disabled {
		return false
	}
	minute := at.Hour()*60 + at.Minute()
	if day.StartMinute < day.EndMinute && minute >= day.StartMinute && minute < day.EndMinute {
		return true
	}
	if day.StartMinute > day.EndMinute && minute >= day.StartMinute {
		return true
	}
	previous := p.day((at.Weekday() + 6) % 7)
	return !previous.Disabled && previous.StartMinute > previous.EndMinute && minute < previous.EndMinute
}

func nextLocalMidnight(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day()+1, 0, 0, 0, 0, at.Location())
}

// Walking actual minute boundaries handles both missing and repeated local
// times during DST. Windows are minute-granular and a weekly policy repeats
// within eight calendar days, so this search is strictly bounded.
func (p userPolicy) nextScheduleState(at time.Time, allowed bool) time.Time {
	until := at.AddDate(0, 0, 8)
	for candidate := at.Truncate(time.Minute).Add(time.Minute); !candidate.After(until); candidate = candidate.Add(time.Minute) {
		if p.scheduleAllows(candidate) == allowed {
			return candidate
		}
	}
	return time.Time{}
}

func evaluatePolicy(p userPolicy, total int, now time.Time) policyDecision {
	day := p.day(now.Weekday())
	quota := day.QuotaSeconds
	if quota > 0 {
		quota += p.BonusSeconds
	}
	d := policyDecision{Action: "allow", Reason: "allowed", QuotaSeconds: quota, Unlimited: quota == 0, NextTransitionAt: nextLocalMidnight(now)}
	if quota > 0 {
		d.RemainingSeconds = max(0, quota-total)
	}
	if p.Paused {
		d.Action, d.Reason = "lock", "paused"
		return d
	}
	if !p.scheduleAllows(now) {
		d.Action, d.Reason = "lock", "schedule"
		d.NextAllowedAt = p.nextScheduleState(now, true)
		if !d.NextAllowedAt.IsZero() && d.NextAllowedAt.Before(d.NextTransitionAt) {
			d.NextTransitionAt = d.NextAllowedAt
		}
	} else if quota > 0 && total >= quota {
		d.Action, d.Reason = "lock", "quota"
		next := nextLocalMidnight(now)
		if p.scheduleAllows(next) {
			d.NextAllowedAt = next
		} else {
			d.NextAllowedAt = p.nextScheduleState(next, true)
		}
	} else {
		// Only the current calendar day can affect this lease. The quota and
		// bonus are recalculated at midnight even for overnight windows.
		for candidate := now.Truncate(time.Minute).Add(time.Minute); candidate.Before(d.NextTransitionAt); candidate = candidate.Add(time.Minute) {
			if !p.scheduleAllows(candidate) {
				d.NextTransitionAt = candidate
				break
			}
		}
		d.LeaseSeconds = 90
		if quota > 0 {
			d.LeaseSeconds = min(d.LeaseSeconds, d.RemainingSeconds)
		}
		d.LeaseSeconds = min(d.LeaseSeconds, max(0, int(d.NextTransitionAt.Sub(now)/time.Second)))
	}
	return d
}
