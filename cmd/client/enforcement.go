package main

import "time"

// All workstation locking goes through this boundary. Test mode still
// evaluates policy, but never calls the supplied operating-system action.
type enforcement struct {
	enabled     bool
	lastAttempt time.Time
	wouldLock   bool
}

func (e *enforcement) poll(blocked bool, now time.Time, lock func(), report func()) {
	if !blocked {
		e.wouldLock = false
		return
	}
	if !e.enabled {
		if !e.wouldLock {
			report()
		}
		e.wouldLock = true
		return
	}
	if !e.lastAttempt.IsZero() && now.Sub(e.lastAttempt) < 3*time.Second {
		return
	}
	e.lastAttempt = now
	lock()
}
