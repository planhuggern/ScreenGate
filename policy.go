package main

import (
	"errors"
	"strings"
	"time"

	"screengate/internal/screenpolicy"
)

var errInvalidPolicy = errors.New("invalid screen-time policy")

type weekdayPolicy = screenpolicy.Weekday

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
	NextLockAt       time.Time
	LeaseSeconds     int
}

func validPolicyUser(user string) bool {
	return strings.TrimSpace(user) != "" && len(user) <= 200
}

func validateWeekdayPolicies(days []weekdayPolicy) error {
	if screenpolicy.ValidateWeekdays(days) != nil {
		return errInvalidPolicy
	}
	return nil
}

func (p userPolicy) rules(date string) *screenpolicy.Rules {
	return &screenpolicy.Rules{DailyQuotaSeconds: p.DailyQuotaSeconds, Paused: p.Paused, Weekdays: p.Weekdays, BonusSeconds: p.BonusSeconds, BonusDate: date}
}

func (p userPolicy) scheduleAllows(at time.Time) bool {
	return p.rules("").Allows(at)
}

func nextLocalMidnight(at time.Time) time.Time {
	return screenpolicy.MidnightAfter(at)
}

func (p userPolicy) nextScheduleState(at time.Time, allowed bool) time.Time {
	return p.rules("").NextState(at, allowed)
}

func evaluatePolicy(p userPolicy, total int, now time.Time) policyDecision {
	quota := p.rules(now.Format("2006-01-02")).QuotaAt(now)
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
		if !p.scheduleAllows(d.NextTransitionAt) {
			d.NextLockAt = d.NextTransitionAt
		}
	}
	return d
}
