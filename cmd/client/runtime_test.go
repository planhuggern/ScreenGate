package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests cover the production loop. TestLoop checks implemented rules;
// TestCurrentLoop still characterizes known deviations from BEHAVIOR_RULES.md.
type loopReply struct {
	status int
	body   string
	err    error
}

type loopRequest struct {
	payload map[string]any
	reply   chan loopReply
}

type loopTransport struct{ requests chan loopRequest }

func (s loopTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return nil, err
	}
	r.Body.Close()
	request := loopRequest{payload: payload, reply: make(chan loopReply, 1)}
	select {
	case s.requests <- request:
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	select {
	case result := <-request.reply:
		if result.err != nil {
			return nil, result.err
		}
		return &http.Response{StatusCode: result.status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(result.body))}, nil
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
}

type loopHarness struct {
	t        *testing.T
	mu       sync.Mutex
	now      time.Time
	path     string
	polls    chan time.Time
	reports  chan time.Time
	requests chan loopRequest
	saved    chan clientState
	session  atomic.Value
	locks    atomic.Int64
	warnings atomic.Int64
	cancel   context.CancelFunc
	done     chan struct{}
}

func startTestClient(t *testing.T, state clientState, now time.Time, path string, locking bool) *loopHarness {
	t.Helper()
	if path == "" {
		path = filepath.Join(t.TempDir(), "state.json")
	}
	if state.Identity == "" {
		state.Identity = "test-client"
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = now
	}
	h := &loopHarness{t: t, now: now, path: path, polls: make(chan time.Time), reports: make(chan time.Time), requests: make(chan loopRequest), saved: make(chan clientState, 1024), done: make(chan struct{})}
	h.session.Store("active")
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	client := newHTTPClient()
	client.Transport = loopTransport{requests: h.requests}
	config := clientConfig{Server: "http://test.invalid/heartbeat", User: "child", DeviceID: "pc", Token: "test-secret"}
	runtime := clientRuntime{
		now: h.time, pollTicks: h.polls, reportTicks: h.reports, httpClient: client,
		save: func(state clientState) error {
			// Exercise real signing/storage and detach the Report pointer before
			// handing a snapshot to the test goroutine.
			if err := saveState(path, config.Token, state); err != nil {
				return err
			}
			data, err := json.Marshal(state)
			if err != nil {
				return err
			}
			var snapshot clientState
			if err := json.Unmarshal(data, &snapshot); err != nil {
				return err
			}
			h.saved <- snapshot
			return nil
		},
		sessionState: func(time.Duration) string { return h.session.Load().(string) },
		lock:         func() { h.locks.Add(1) },
		showWarning:  func(screenTimeWarning) error { h.warnings.Add(1); return nil },
		foregroundApp: func() (string, error) {
			return "", errors.New("unexpected app tracking in loop test")
		},
	}
	go func() {
		defer close(h.done)
		runClient(ctx, config, state, enforcement{enabled: locking}, runtime)
	}()
	t.Cleanup(h.stop)
	return h
}

func (h *loopHarness) time() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *loopHarness) stop() {
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		h.t.Fatal("client loop did not stop")
	}
}

func (h *loopHarness) send(ticks chan time.Time) {
	h.t.Helper()
	select {
	case ticks <- h.time():
	case <-time.After(5 * time.Second):
		h.t.Fatal("client loop did not accept a tick")
	}
}

func (h *loopHarness) request() loopRequest {
	h.t.Helper()
	select {
	case r := <-h.requests:
		return r
	case <-time.After(5 * time.Second):
		h.t.Fatal("client did not send a heartbeat")
		return loopRequest{}
	}
}

func (h *loopHarness) await(matches func(clientState) bool) clientState {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case state := <-h.saved:
			if matches(state) {
				// A tick can only be received after the preceding result/poll
				// has finished, including enforcement after its save.
				h.send(h.polls)
				return state
			}
		case <-deadline:
			h.t.Fatal("client did not persist the expected transition")
			return clientState{}
		}
	}
}

func (h *loopHarness) poll(after time.Duration) clientState {
	h.t.Helper()
	h.mu.Lock()
	h.now = h.now.Add(after)
	now := h.now
	h.mu.Unlock()
	h.send(h.polls)
	return h.await(func(s clientState) bool { return s.UpdatedAt.Equal(now) })
}

func allowReply(remaining int) loopReply {
	data, _ := json.Marshal(response{Action: "allow", Reason: "allowed", PolicyDate: "2026-09-18", PolicyVersion: 1, QuotaSeconds: 120, RemainingSeconds: remaining, LeaseSeconds: 90})
	return loopReply{status: 200, body: string(data)}
}

func establishedState(action string) clientState {
	state := clientState{}
	state.apply(response{Action: action, Reason: "initial", QuotaSeconds: 120, RemainingSeconds: 100, LeaseSeconds: 90}, testMoment(), testMoment())
	return state
}

func TestLoopContactFailuresPreserveLastDecision(t *testing.T) {
	for _, initial := range []string{"fresh", "allow", "lock"} {
		for _, failure := range []struct {
			name  string
			reply loopReply
			auth  bool
		}{
			{"network error", loopReply{err: errors.New("simulated network outage")}, false},
			{"server unavailable", loopReply{status: 503, body: `{}`}, false},
			{"invalid response", loopReply{status: 200, body: `{"action":"unknown"}`}, false},
			{"unauthorized", loopReply{status: 401, body: `{}`}, true},
			{"forbidden", loopReply{status: 403, body: `{}`}, true},
		} {
			t.Run(initial+"/"+failure.name, func(t *testing.T) {
				state := clientState{Action: "lock", Reason: "awaiting_server"}
				if initial != "fresh" {
					state = establishedState(initial)
				}
				h := startTestClient(t, state, testMoment(), "", true)
				h.request().reply <- failure.reply
				got := h.await(func(s clientState) bool {
					if failure.auth {
						return s.Reason == "authentication_failed"
					}
					return s.ServerUnavailable
				})
				if got.ServerUnavailable != !failure.auth {
					t.Fatal("connection status was confused with the last decision")
				}
				wantBlocked := failure.auth || initial != "allow"
				if got.allowed(h.time()) == wantBlocked || (h.locks.Load() > 0) != wantBlocked {
					t.Fatalf("state=%+v locks=%d wantBlocked=%t", got, h.locks.Load(), wantBlocked)
				}
			})
		}
	}
}

func TestLoopAuthenticationFailureAfterOutageLocks(t *testing.T) {
	h := startTestClient(t, establishedState("lock"), testMoment(), "", true)
	h.request().reply <- loopReply{status: 503, body: `{}`}
	h.await(func(s clientState) bool { return s.ServerUnavailable })
	h.send(h.reports)
	h.request().reply <- loopReply{status: 401, body: `{}`}
	state := h.await(func(s clientState) bool { return s.Reason == "authentication_failed" })
	if state.Action != "lock" || state.allowed(h.time()) || h.locks.Load() == 0 {
		t.Fatalf("authentication failure must remain closed after outage, state=%+v locks=%d", state, h.locks.Load())
	}
}

func TestLoopOfflineExhaustionLocks(t *testing.T) {
	h := startTestClient(t, clientState{}, testMoment(), "", true)
	h.request().reply <- allowReply(3)
	h.await(func(s clientState) bool { return s.ServerContacted && s.Report == nil })
	h.send(h.reports)
	h.request().reply <- loopReply{status: 503, body: `{}`}
	h.await(func(s clientState) bool { return s.ServerUnavailable })
	var state clientState
	for i := 0; i < 4; i++ {
		state = h.poll(time.Second)
	}
	if state.RemainingSeconds != 0 || state.allowed(h.time()) || h.locks.Load() == 0 {
		t.Fatalf("exhausted local quota must lock, state=%+v locks=%d", state, h.locks.Load())
	}
}

func TestCurrentLoopReconnectCanReturnLocallyUsedTime(t *testing.T) {
	h := startTestClient(t, clientState{}, testMoment(), "", true)
	h.request().reply <- allowReply(100)
	h.await(func(s clientState) bool { return s.ServerContacted && s.Report == nil })
	for i := 0; i < 5; i++ {
		h.poll(time.Second)
	}
	h.send(h.reports)
	failed := h.request()
	failed.reply <- loopReply{status: 503, body: `{}`}
	h.await(func(s clientState) bool { return s.ServerUnavailable })
	var before clientState
	for i := 0; i < 5; i++ {
		before = h.poll(time.Second)
	}
	h.send(h.reports)
	retry := h.request()
	if retry.payload["heartbeat_id"] != failed.payload["heartbeat_id"] || retry.payload["active_seconds"] != nil {
		t.Fatalf("unexpected wire report: %+v", retry.payload)
	}
	retry.reply <- allowReply(100)
	after := h.await(func(s clientState) bool { return s.ServerContacted && s.Report == nil })
	if before.RemainingSeconds != 90 || after.RemainingSeconds != 95 {
		t.Fatalf("expected current reconciliation gap: before=%d after=%d", before.RemainingSeconds, after.RemainingSeconds)
	}
}

func TestLoopRestartPreservesOfflineQuotaAndAccess(t *testing.T) {
	h := startTestClient(t, establishedState("allow"), testMoment(), "", true)
	h.request().reply <- loopReply{status: 503, body: `{}`}
	h.await(func(s clientState) bool { return s.ServerUnavailable })
	h.poll(time.Second)
	h.stop()
	restartAt := h.time().Add(time.Hour)
	state, err := loadState(h.path, "test-client", "test-secret", restartAt)
	if err != nil {
		t.Fatal(err)
	}
	if state.RemainingSeconds != 99 || !state.ServerContacted || !state.allowed(restartAt) || !state.ServerUnavailable {
		t.Fatalf("unexpected restored outage state: %+v", state)
	}
	restarted := startTestClient(t, state, restartAt, h.path, true)
	restarted.request() // Keep the startup request pending.
	after := restarted.poll(time.Second)
	if restarted.locks.Load() != 0 || !after.allowed(restarted.time()) || after.RemainingSeconds != 98 {
		t.Fatalf("restart must preserve access and consumed quota: state=%+v locks=%d", after, restarted.locks.Load())
	}
}

func TestCurrentLoopLocalEnforcementContinuesDuringPendingRequest(t *testing.T) {
	for _, locking := range []bool{true, false} {
		t.Run(map[bool]string{true: "locking", false: "test mode"}[locking], func(t *testing.T) {
			h := startTestClient(t, clientState{}, testMoment(), "", locking)
			h.request().reply <- allowReply(2)
			h.await(func(s clientState) bool { return s.ServerContacted && s.Report == nil })
			h.send(h.reports)
			pending := h.request()
			h.poll(time.Second)
			state := h.poll(time.Second)
			if state.RemainingSeconds != 0 || (h.locks.Load() > 0) != locking {
				t.Fatalf("pending network should not block enforcement: state=%+v locks=%d", state, h.locks.Load())
			}
			pending.reply <- allowReply(100)
			h.await(func(s clientState) bool { return s.RemainingSeconds == 98 && s.Report == nil })
		})
	}
}

func TestCurrentLoopLockedFallbackStopsCountingAndSuppressesLock(t *testing.T) {
	h := startTestClient(t, establishedState("lock"), testMoment(), "", true)
	// The Windows adapter returns "locked" when its WTS result cannot be
	// read/interpreted. Feed that same output to the real loop. This does not
	// pretend to execute or diagnose an actual Windows API failure.
	if wtsSessionUnlocked(nil, 7) {
		t.Fatal("missing WTS data unexpectedly recognized as unlocked")
	}
	h.session.Store("locked")
	h.request().reply <- loopReply{status: 403, body: `{}`}
	h.await(func(s clientState) bool { return s.Reason == "authentication_failed" })
	var state clientState
	for i := 0; i < 4; i++ {
		state = h.poll(time.Second)
	}
	if state.PendingSeconds != 0 || h.locks.Load() != 0 {
		t.Fatalf("locked fallback should suppress both counting and locking: state=%+v locks=%d", state, h.locks.Load())
	}
}

func TestCurrentLoopSendsChangedSessionAfterPendingResponse(t *testing.T) {
	h := startTestClient(t, establishedState("allow"), testMoment(), "", true)
	first := h.request()
	if first.payload["locked"] != false {
		t.Fatalf("startup report=%+v", first.payload)
	}
	h.session.Store("locked")
	h.poll(time.Second)
	first.reply <- allowReply(100)
	locked := h.request() // The earlier request was in flight during the poll.
	if locked.payload["locked"] != true || locked.payload["heartbeat_id"] == first.payload["heartbeat_id"] {
		t.Fatalf("session change was not re-reported: first=%+v next=%+v", first.payload, locked.payload)
	}
	locked.reply <- allowReply(100)
	h.await(func(s clientState) bool { return s.ServerContacted && s.Report == nil })
	h.session.Store("active")
	h.poll(time.Second)
	unlocked := h.request()
	if unlocked.payload["locked"] != false || unlocked.payload["heartbeat_id"] == locked.payload["heartbeat_id"] {
		t.Fatalf("unlock did not send a new report: %+v", unlocked.payload)
	}
	unlocked.reply <- loopReply{status: 200, body: `{"action":"lock","reason":"paused","quota_seconds":120,"remaining_seconds":100,"policy_version":2}`}
	h.await(func(s clientState) bool { return s.Reason == "paused" })
	if h.locks.Load() != 1 {
		t.Fatalf("server pause after unlock did not trigger lock: %d", h.locks.Load())
	}
}

func TestCurrentLoopCountingWarningsAndSuspension(t *testing.T) {
	h := startTestClient(t, clientState{}, testMoment(), "", true)
	h.request().reply <- allowReply(61)
	h.await(func(s clientState) bool { return s.ServerContacted && s.Report == nil })
	state := h.poll(time.Second)
	if state.RemainingSeconds != 60 || h.warnings.Load() != 1 {
		t.Fatalf("unlocked use/warning: remaining=%d warnings=%d", state.RemainingSeconds, h.warnings.Load())
	}
	h.session.Store("locked")
	h.poll(time.Second) // Finish the preceding unlocked second.
	h.request()         // Leave this network operation pending.
	for i := 0; i < 3; i++ {
		state = h.poll(time.Second)
	}
	if state.RemainingSeconds != 59 || h.locks.Load() != 0 {
		t.Fatalf("locked use: remaining=%d locks=%d", state.RemainingSeconds, h.locks.Load())
	}
	h.session.Store("active")
	state = h.poll(time.Hour) // A suspended process must not bill the gap.
	if state.RemainingSeconds != 59 || h.locks.Load() != 0 {
		t.Fatalf("sleep must preserve rest quota without billing the gap: remaining=%d locks=%d", state.RemainingSeconds, h.locks.Load())
	}
}
