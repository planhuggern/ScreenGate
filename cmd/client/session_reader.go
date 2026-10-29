package main

import (
	"strings"
	"time"

	"screengate/internal/sessiondiag"
)

type sessionObservation struct {
	State   string
	Failure *sessiondiag.Failure
}

// Windows supplies these calls. Tests exercise the same reading order and
// fallback behavior with explicit results, without touching a real desktop.
type sessionReader struct {
	sessionID    func() (uint32, *sessiondiag.Failure)
	sessionInfo  func(uint32) ([]byte, *sessiondiag.Failure)
	inputDesktop func() (uintptr, *sessiondiag.Failure)
	closeDesktop func(uintptr)
	desktopName  func(uintptr) (string, *sessiondiag.Failure)
	idleDuration func() (time.Duration, *sessiondiag.Failure)
}

func (r sessionReader) observe(idleTimeout time.Duration) sessionObservation {
	failed := func(f *sessiondiag.Failure) sessionObservation {
		return sessionObservation{State: "locked", Failure: f}
	}
	id, failure := r.sessionID()
	if failure != nil {
		return failed(failure)
	}
	info, failure := r.sessionInfo(id)
	if failure != nil {
		return failed(failure)
	}
	if reason := sessionInfoError(info, id); reason != "" {
		return failed(&sessiondiag.Failure{Call: "WTSQuerySessionInformationW", Reason: reason})
	}
	if !wtsSessionUnlocked(info, id) {
		return sessionObservation{State: "locked"}
	}
	desktop, failure := r.inputDesktop()
	if failure != nil {
		return failed(failure)
	}
	defer r.closeDesktop(desktop)
	name, failure := r.desktopName(desktop)
	if failure != nil {
		return failed(failure)
	}
	if name == "" {
		return failed(&sessiondiag.Failure{Call: "GetUserObjectInformationW", Reason: "empty_desktop_name"})
	}
	if !strings.EqualFold(name, "Default") {
		return sessionObservation{State: "locked"}
	}
	if idleTimeout > 0 {
		idle, failure := r.idleDuration()
		if failure != nil {
			// Preserve the existing active fallback when only idle reading fails.
			return sessionObservation{State: "active", Failure: failure}
		}
		if idle >= idleTimeout {
			return sessionObservation{State: "idle"}
		}
	}
	return sessionObservation{State: "active"}
}
