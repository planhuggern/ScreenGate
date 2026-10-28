// Package screenpolicy contains the calendar rules shared by server and client.
package screenpolicy

import (
	"errors"
	"time"
)

// An unconfigured weekday inherits the default quota and allows all day.
// Zero quota means unlimited; Disabled prohibits the entire calendar day.
type Weekday struct {
	Weekday      int  `json:"weekday"`
	Configured   bool `json:"configured"`
	QuotaSeconds int  `json:"quota_seconds"`
	Disabled     bool `json:"disabled"`
	StartMinute  int  `json:"start_minute"`
	EndMinute    int  `json:"end_minute"`
}

// Rules is a snapshot. A bonus belongs only to BonusDate; pause has no expiry.
type Rules struct {
	DailyQuotaSeconds int       `json:"daily_quota_seconds"`
	Paused            bool      `json:"paused"`
	Weekdays          []Weekday `json:"weekdays"`
	BonusSeconds      int       `json:"bonus_seconds"`
	BonusDate         string    `json:"bonus_date"`
}

func ValidateWeekdays(days []Weekday) error {
	seen := [7]bool{}
	for _, day := range days {
		if day.Weekday < 0 || day.Weekday > 6 || seen[day.Weekday] {
			return errors.New("invalid or duplicate weekday")
		}
		seen[day.Weekday] = true
		if day.Configured && (day.QuotaSeconds < 0 || day.QuotaSeconds > 86400 || day.StartMinute < 0 || day.StartMinute > 1439 || day.EndMinute < 0 || day.EndMinute > 1440 || day.StartMinute == day.EndMinute) {
			return errors.New("invalid weekday quota or time window")
		}
	}
	return nil
}

func (r Rules) Validate() error {
	if r.DailyQuotaSeconds < 0 || r.DailyQuotaSeconds > 86400 || r.BonusSeconds < 0 || r.BonusSeconds > 86400 {
		return errors.New("invalid policy quota or bonus")
	}
	if _, err := time.Parse("2006-01-02", r.BonusDate); err != nil {
		return errors.New("invalid bonus date")
	}
	return ValidateWeekdays(r.Weekdays)
}

func (r Rules) Day(weekday time.Weekday) Weekday {
	for _, day := range r.Weekdays {
		if day.Weekday == int(weekday) && day.Configured {
			return day
		}
	}
	return Weekday{Weekday: int(weekday), QuotaSeconds: r.DailyQuotaSeconds, EndMinute: 1440}
}

func (r Rules) QuotaAt(at time.Time) int {
	quota := r.Day(at.Weekday()).QuotaSeconds
	if quota > 0 && at.Format("2006-01-02") == r.BonusDate {
		quota += r.BonusSeconds
	}
	return quota
}

func (r Rules) Allows(at time.Time) bool {
	day := r.Day(at.Weekday())
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
	previous := r.Day((at.Weekday() + 6) % 7)
	return !previous.Disabled && previous.StartMinute > previous.EndMinute && minute < previous.EndMinute
}

func MidnightAfter(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day()+1, 0, 0, 0, 0, at.Location())
}

// Walk actual minute boundaries to handle missing and repeated DST times.
// A weekly schedule repeats within eight calendar days.
func (r Rules) NextState(at time.Time, allowed bool) time.Time {
	until := at.AddDate(0, 0, 8)
	for candidate := at.Truncate(time.Minute).Add(time.Minute); !candidate.After(until); candidate = candidate.Add(time.Minute) {
		if r.Allows(candidate) == allowed {
			return candidate
		}
	}
	return time.Time{}
}
