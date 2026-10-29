package main

import (
	"bytes"
	"encoding/json"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"screengate/internal/sessiondiag"
)

func TestSessionDiagnosticsLogFailureRemindersAndRecovery(t *testing.T) {
	var output bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previousOutput)
	now := testMoment()
	observation := sessionObservation{State: "active"}
	client := clientLoop{runtime: clientRuntime{sessionState: func(time.Duration) sessionObservation { return observation }}}
	client.observeSession(now)
	observation = sessionObservation{State: "locked", Failure: &sessiondiag.Failure{Call: "WTSQuerySessionInformationW", Reason: "truncated_response"}}
	client.observeSession(now.Add(time.Second))
	client.observeSession(now.Add(31 * time.Second))
	client.observeSession(now.Add(61 * time.Second))
	if strings.Count(output.String(), "session_read_error=true") != 2 || !strings.Contains(output.String(), "windows_error=unavailable fallback=locked") {
		t.Fatalf("failure should log immediately and once per minute without inventing a code: %s", &output)
	}
	report := client.state.SessionDiagnostics
	if !report.Valid() || !report.Failed || report.LastSuccess.State != "active" || !report.LastSuccess.At.Equal(now) {
		t.Fatal("failed read replaced the last confirmed observation")
	}
	observation = sessionObservation{State: "locked"}
	client.observeSession(now.Add(62 * time.Second))
	if !strings.Contains(output.String(), "session_read_recovered=true") || client.state.SessionDiagnostics.Failed || client.state.SessionDiagnostics.LastError == nil {
		t.Fatal("recovery lost the failure history or was not logged")
	}
}

func TestLoopFailedReadPersistsDiagnosticsAndPreservesLockedFallback(t *testing.T) {
	h := startTestClient(t, establishedState("allow"), testMoment(), "", true)
	h.request().reply <- allowReply(100)
	h.await(func(s clientState) bool { return s.ServerContacted && s.Report == nil })
	h.session.Store("locked")
	code := uint32(5)
	h.sessionFailure.Store(&sessiondiag.Failure{Call: "OpenInputDesktop", Reason: "call_failed", Code: &code})
	h.poll(time.Second)
	request := h.request()
	diagnostics := request.payload["session_diagnostics"].(map[string]any)
	if diagnostics["failed"] != true || request.payload["locked"] != true {
		t.Fatal("wire report lost the error or changed the fallback")
	}
	request.reply <- loopReply{status: 503, body: `{}`}
	h.await(func(s clientState) bool { return s.ServerUnavailable })
	var state clientState
	for i := 0; i < 35; i++ {
		state = h.poll(time.Second)
	}
	if state.RemainingSeconds != 99 || h.locks.Load() != 0 || !state.SessionDiagnostics.Failed {
		t.Fatal("observation introduced counting or locking during the locked fallback")
	}
	restored, err := loadState(h.path, "test-client", "test-secret", h.time())
	if err != nil || restored.SessionDiagnostics == nil || !restored.SessionDiagnostics.Failed || restored.SessionDiagnostics.LastError.Code == nil {
		t.Fatalf("offline diagnostics did not survive restart: %v", err)
	}
}

func TestLoopReportsBriefFailureAfterRecoveryWhileRequestWasPending(t *testing.T) {
	h := startTestClient(t, establishedState("allow"), testMoment(), "", false)
	initial := h.request()
	h.sessionFailure.Store(&sessiondiag.Failure{Call: "GetLastInputInfo", Reason: "call_failed"})
	h.poll(time.Second) // Fallback is still active: no session-state change.
	h.sessionFailure.Store(nil)
	h.poll(time.Second)
	initial.reply <- allowReply(100)
	next := h.request()
	data, err := json.Marshal(next.payload["session_diagnostics"])
	var report sessiondiag.Report
	if err != nil || json.Unmarshal(data, &report) != nil || !report.Valid() || report.Failed || report.LastError.Call != "GetLastInputInfo" {
		t.Fatalf("brief pending/offline failure disappeared: %s", data)
	}
	if next.payload["locked"] != false || h.locks.Load() != 0 {
		t.Fatal("diagnostics changed session state or test-mode locking")
	}
	next.reply <- allowReply(98)
}

func TestInvalidSavedDiagnosticsDoNotDiscardUsableQuota(t *testing.T) {
	state := establishedState("allow")
	state.Identity = "client"
	state.SessionDiagnostics = &sessiondiag.Report{Failed: true}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, "token", state); err != nil {
		t.Fatal(err)
	}
	restored, err := loadState(path, "client", "token", testMoment())
	if err != nil || restored.SessionDiagnostics != nil || restored.RemainingSeconds != 100 || !restored.allowed(testMoment()) {
		t.Fatalf("optional diagnostics changed authorization: %+v %v", restored, err)
	}
}
