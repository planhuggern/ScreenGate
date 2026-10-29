package main

import (
	"fmt"
	"log"
	"time"

	"screengate/internal/sessiondiag"
)

func sameSessionFailure(a, b *sessiondiag.Failure) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Call != b.Call || a.Reason != b.Reason || (a.Code == nil) != (b.Code == nil) {
		return false
	}
	return a.Code == nil || *a.Code == *b.Code
}

// Keep diagnostics separate from sessionState, the existing counting/locking
// input. A failed read must neither invent a confirmed lock nor change fallback.
func (c *clientLoop) observeSession(now time.Time) (string, bool) {
	observation := c.runtime.sessionState(c.runtime.idleTimeout)
	previous := c.state.SessionDiagnostics
	report := &sessiondiag.Report{ObservedAt: now, Failed: observation.Failure != nil}
	if previous != nil {
		report.LastError, report.LastSuccess = previous.LastError, previous.LastSuccess
	}
	changed := previous == nil || previous.Failed != report.Failed
	if observation.Failure != nil {
		failure := *observation.Failure
		failure.At = now
		report.LastError = &failure
		if previous != nil && !sameSessionFailure(previous.LastError, &failure) {
			changed = true
		}
		if changed || now.Sub(c.lastSessionLog) >= time.Minute {
			code := "unavailable"
			if failure.Code != nil {
				code = fmt.Sprint(*failure.Code)
			}
			log.Printf("session_read_error=true observed_at=%s call=%s reason=%s windows_error=%s fallback=%s", now.Format(time.RFC3339Nano), failure.Call, failure.Reason, code, observation.State)
			c.lastSessionLog = now
		}
	} else {
		report.LastSuccess = &sessiondiag.Success{At: now, State: observation.State}
		if previous != nil && previous.Failed {
			log.Printf("session_read_recovered=true observed_at=%s state=%s", now.Format(time.RFC3339Nano), observation.State)
		}
	}
	c.state.SessionDiagnostics = report
	if changed {
		c.diagnosticRevision++
	}
	return observation.State, changed
}
