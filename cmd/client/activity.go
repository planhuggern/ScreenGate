package main

import "time"

type activityMeter struct {
	lastAt   time.Time
	active   bool
	fraction time.Duration
}

func (m *activityMeter) update(active bool, now time.Time) int {
	seconds := 0
	if !m.lastAt.IsZero() && m.active {
		elapsed := now.Sub(m.lastAt)
		// A suspended PC or stalled process must not bill the sleeping interval.
		if elapsed > 0 && elapsed <= 5*time.Second {
			m.fraction += elapsed
			seconds = int(m.fraction / time.Second)
			m.fraction %= time.Second
		}
	}
	m.lastAt, m.active = now, active
	return seconds
}
