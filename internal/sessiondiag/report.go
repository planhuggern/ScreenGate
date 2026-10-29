// Package sessiondiag defines the bounded diagnostics shared by client and monitor.
package sessiondiag

import "time"

type Failure struct {
	At     time.Time `json:"at"`
	Call   string    `json:"call"`
	Reason string    `json:"reason"`
	Code   *uint32   `json:"code,omitempty"`
}

type Success struct {
	At    time.Time `json:"at"`
	State string    `json:"state"`
}

// LastSuccess is history, never a substitute for a failed current observation.
// LastError survives recovery so a brief offline failure can be reported later.
type Report struct {
	ObservedAt  time.Time `json:"observed_at"`
	Failed      bool      `json:"failed"`
	LastError   *Failure  `json:"last_error,omitempty"`
	LastSuccess *Success  `json:"last_success,omitempty"`
}

func (r Report) Valid() bool {
	if r.ObservedAt.IsZero() {
		return false
	}
	if e := r.LastError; e != nil {
		if e.At.IsZero() || e.At.After(r.ObservedAt) || !knownCall(e.Call) || !knownReason(e.Reason) {
			return false
		}
	}
	if s := r.LastSuccess; s != nil {
		if s.At.IsZero() || s.At.After(r.ObservedAt) || (s.State != "locked" && s.State != "active" && s.State != "idle") {
			return false
		}
	}
	if r.Failed {
		return r.LastError != nil && r.LastError.At.Equal(r.ObservedAt)
	}
	return r.LastSuccess != nil && r.LastSuccess.At.Equal(r.ObservedAt)
}

// The public monitor must not receive arbitrary client text, paths or names.
func knownCall(call string) bool {
	switch call {
	case "ProcessIdToSessionId", "WTSQuerySessionInformationW", "OpenInputDesktop", "GetUserObjectInformationW", "GetLastInputInfo":
		return true
	}
	return false
}

func knownReason(reason string) bool {
	switch reason {
	case "call_failed", "empty_response", "truncated_response", "unknown_level", "session_mismatch", "unknown_session_state", "unknown_lock_flag", "empty_desktop_name":
		return true
	}
	return false
}
