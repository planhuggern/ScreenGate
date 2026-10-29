package main

import (
	"testing"
	"time"

	"screengate/internal/sessiondiag"
)

func healthySessionReader() sessionReader {
	return sessionReader{
		sessionID:    func() (uint32, *sessiondiag.Failure) { return 7, nil },
		sessionInfo:  func(uint32) ([]byte, *sessiondiag.Failure) { return sessionInfoFixture(1, 7, 0, 1), nil },
		inputDesktop: func() (uintptr, *sessiondiag.Failure) { return 1, nil },
		closeDesktop: func(uintptr) {},
		desktopName:  func(uintptr) (string, *sessiondiag.Failure) { return "Default", nil },
		idleDuration: func() (time.Duration, *sessiondiag.Failure) { return 0, nil },
	}
}

func TestSessionReadingReportsFailuresWithoutChangingFallback(t *testing.T) {
	for _, call := range []string{"ProcessIdToSessionId", "WTSQuerySessionInformationW", "OpenInputDesktop", "GetUserObjectInformationW", "GetLastInputInfo"} {
		t.Run(call, func(t *testing.T) {
			reader := healthySessionReader()
			code := uint32(5)
			failure := &sessiondiag.Failure{Call: call, Reason: "call_failed", Code: &code}
			closed := false
			reader.closeDesktop = func(uintptr) { closed = true }
			want := "locked"
			switch call {
			case "ProcessIdToSessionId":
				reader.sessionID = func() (uint32, *sessiondiag.Failure) { return 0, failure }
			case "WTSQuerySessionInformationW":
				reader.sessionInfo = func(uint32) ([]byte, *sessiondiag.Failure) { return nil, failure }
			case "OpenInputDesktop":
				reader.inputDesktop = func() (uintptr, *sessiondiag.Failure) { return 0, failure }
			case "GetUserObjectInformationW":
				reader.desktopName = func(uintptr) (string, *sessiondiag.Failure) { return "", failure }
			case "GetLastInputInfo":
				reader.idleDuration = func() (time.Duration, *sessiondiag.Failure) { return 0, failure }
				want = "active"
			}
			got := reader.observe(time.Minute)
			if got.State != want || got.Failure != failure {
				t.Fatalf("lost diagnostics or changed fallback: %+v", got)
			}
			if closed != (call == "GetUserObjectInformationW" || call == "GetLastInputInfo") {
				t.Fatal("input desktop was leaked or closed without being opened")
			}
		})
	}
}

func TestSessionReadingSeparatesMalformedDataFromConfirmedLock(t *testing.T) {
	for _, test := range []struct {
		name          string
		info          []byte
		reason, state string
	}{
		{"truncated", nil, "truncated_response", "locked"},
		{"level", sessionInfoFixture(2, 7, 0, 1), "unknown_level", "locked"},
		{"other session", sessionInfoFixture(1, 8, 0, 1), "session_mismatch", "locked"},
		{"unknown state", sessionInfoFixture(1, 7, 99, 1), "unknown_session_state", "locked"},
		{"unknown flag", sessionInfoFixture(1, 7, 0, 0xffffffff), "unknown_lock_flag", "locked"},
		{"confirmed lock", sessionInfoFixture(1, 7, 0, 0), "", "locked"},
		{"disconnected", sessionInfoFixture(1, 7, 4, 1), "", "locked"},
		{"unlocked", sessionInfoFixture(1, 7, 0, 1), "", "active"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := healthySessionReader()
			reader.sessionInfo = func(uint32) ([]byte, *sessiondiag.Failure) { return test.info, nil }
			got := reader.observe(time.Minute)
			if got.State != test.state || (got.Failure != nil) != (test.reason != "") {
				t.Fatalf("wrong session observation: %+v", got)
			}
			if got.Failure != nil && (got.Failure.Reason != test.reason || got.Failure.Code != nil) {
				t.Fatal("invented Windows code or wrong interpretation failure")
			}
		})
	}
	reader := healthySessionReader()
	reader.desktopName = func(uintptr) (string, *sessiondiag.Failure) { return "Winlogon", nil }
	if got := reader.observe(time.Minute); got.State != "locked" || got.Failure != nil {
		t.Fatal("a recognized non-default desktop is not a reading failure")
	}
	reader.desktopName = func(uintptr) (string, *sessiondiag.Failure) { return "Default", nil }
	reader.idleDuration = func() (time.Duration, *sessiondiag.Failure) { return time.Minute, nil }
	if got := reader.observe(time.Minute); got.State != "idle" || got.Failure != nil {
		t.Fatal("idle detection changed")
	}
}
