package main

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestLoopOfflineQuotaContinuesBeyondNinetySeconds(t *testing.T) {
	h := startTestClient(t, clientState{}, testMoment(), "", true)
	h.request().reply <- loopReply{status: 200, body: `{"action":"allow","reason":"allowed","quota_seconds":600,"remaining_seconds":300,"lease_seconds":90,"policy_date":"2026-09-18"}`}
	h.await(func(s clientState) bool { return s.ServerContacted && s.Report == nil })
	h.send(h.reports)
	h.request().reply <- loopReply{status: http.StatusServiceUnavailable, body: `{}`}
	h.await(func(s clientState) bool { return s.ServerUnavailable })

	var state clientState
	for second := 0; second < 100; second++ {
		state = h.poll(time.Second)
	}
	if state.RemainingSeconds != 200 || !state.allowed(h.time()) || h.locks.Load() != 0 {
		t.Fatalf("100 seconds of offline use must leave 200 seconds: remaining=%d locks=%d", state.RemainingSeconds, h.locks.Load())
	}
}

func TestLoopContactFailurePreservesKnownClosingTime(t *testing.T) {
	state := clientState{}
	now := testMoment()
	state.apply(response{Action: "allow", QuotaSeconds: 600, RemainingSeconds: 300, LeaseSeconds: 90, ServerTime: now, NextLockAt: now.Add(3 * time.Second)}, now, now)
	h := startTestClient(t, state, now, "", true)
	h.request().reply <- loopReply{status: 503, body: `{}`}
	h.await(func(s clientState) bool { return s.ServerUnavailable })
	h.poll(time.Second)
	h.poll(time.Second)
	if h.locks.Load() != 0 {
		t.Fatal("closed before the known closing time")
	}
	state = h.poll(time.Second)
	if state.RemainingSeconds != 297 || h.locks.Load() != 1 {
		t.Fatal("contact failure erased the known closing time")
	}
}

func TestLoopWithoutStoredRulesRetriesWhenSessionUnlocks(t *testing.T) {
	now := testMoment()
	path := filepath.Join(t.TempDir(), "missing-state.json")
	state, err := loadState(path, "test-client", "test-secret", now)
	if err != nil {
		t.Fatal(err)
	}
	h := startTestClient(t, state, now, path, true)
	initial := h.request() // Startup attempts contact immediately.
	h.poll(time.Second)
	if h.locks.Load() == 0 {
		t.Fatal("missing rules allowed use while awaiting the server")
	}
	h.session.Store("locked")
	h.poll(time.Second)
	initial.reply <- loopReply{status: 503, body: `{}`}
	lockedReport := h.request() // Report the session change after the first request.
	h.session.Store("active")
	state = h.poll(time.Second)
	if state.allowed(h.time()) {
		t.Fatal("unlock without stored rules granted access")
	}
	lockedReport.reply <- loopReply{status: 503, body: `{}`}
	unlockedReport := h.request()
	if unlockedReport.payload["locked"] != false {
		t.Fatal("unlock did not trigger another heartbeat attempt")
	}
	unlockedReport.reply <- allowReply(100)
	state = h.await(func(s clientState) bool { return s.ServerContacted && s.Report == nil })
	if !state.allowed(h.time()) || state.ServerUnavailable {
		t.Fatal("new server decision did not restore allowed access")
	}
}

func TestLoopOfflineExhaustionInTestModeNeverLocksWindows(t *testing.T) {
	h := startTestClient(t, clientState{}, testMoment(), "", false)
	h.request().reply <- allowReply(2)
	h.await(func(s clientState) bool { return s.ServerContacted && s.Report == nil })
	h.send(h.reports)
	h.request().reply <- loopReply{status: 503, body: `{}`}
	h.await(func(s clientState) bool { return s.ServerUnavailable })
	h.poll(time.Second)
	state := h.poll(time.Second)
	if state.RemainingSeconds != 0 || state.allowed(h.time()) || h.locks.Load() != 0 {
		t.Fatal("test mode must evaluate exhausted offline quota without locking Windows")
	}
}

func TestLegacyFailOpenStateRequiresFreshDecision(t *testing.T) {
	state := clientState{Identity: "test-client", Action: "allow", Reason: "server_unavailable", QuotaSeconds: 600, RemainingSeconds: 300, UpdatedAt: testMoment()}
	path := filepath.Join(t.TempDir(), "legacy-state.json")
	if err := saveState(path, "test-secret", state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(path, "test-client", "test-secret", testMoment())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.allowed(testMoment()) || loaded.Reason != "legacy_offline_state" || loaded.RemainingSeconds != 300 {
		t.Fatal("old fail-open state invented a last server decision")
	}
	loaded.markServerUnavailable()
	if loaded.allowed(testMoment()) {
		t.Fatal("another contact failure reopened ambiguous legacy state")
	}
}
