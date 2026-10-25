package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"
)

// These are the existing loop's operating-system, clock, network and storage
// boundaries. Production and characterization tests run the same event loop.
type clientRuntime struct {
	now           func() time.Time
	pollTicks     <-chan time.Time
	reportTicks   <-chan time.Time
	httpClient    *http.Client
	save          func(clientState) error
	sessionState  func(time.Duration) string
	lock          func()
	showWarning   func(screenTimeWarning) error
	foregroundApp func() (string, error)
	idleTimeout   time.Duration
	trackApps     bool
}

func newFocusEvent(deviceID, username, app string, activeSeconds int, timestamp time.Time) focusEvent {
	return focusEvent{Type: "focus_changed", DeviceID: deviceID, User: username, PreviousApp: app, ActiveSeconds: activeSeconds, Timestamp: timestamp}
}

type heartbeatResult struct {
	sessionState string
	response     response
	sentAt       time.Time
	err          error
}

func runClient(ctx context.Context, config clientConfig, state clientState, guard enforcement, runtime clientRuntime) {
	persist := func() bool {
		if err := runtime.save(state); err != nil {
			state.invalidate("state_unavailable")
			log.Printf("cannot persist authorization: %v", err)
			return false
		}
		return true
	}
	persist()
	client := runtime.httpClient
	eventEndpoint := strings.TrimSuffix(config.Server, "/heartbeat") + "/event"
	results := make(chan heartbeatResult, 1)
	eventResults := make(chan error, 1)
	heartbeatInFlight, eventInFlight := false, false
	sessionState := "locked"
	warnings := warningState{}
	tracker := focusTracker{}
	meter := activityMeter{}
	var pendingEvents []focusEvent
	lastStatus := ""
	lastPoll := runtime.now()

	queueEvent := func(app string, seconds int, now time.Time) {
		if app == "" || seconds <= 0 || !runtime.trackApps {
			return
		}
		if len(pendingEvents) >= 256 {
			return
		}
		pendingEvents = append(pendingEvents, newFocusEvent(config.DeviceID, config.User, app, min(seconds, 86400), now))
	}
	flushFocus := func(now time.Time) {
		if app, seconds, ok := tracker.finish(now); ok {
			queueEvent(app, seconds, now)
		}
	}
	sendEvent := func() {
		if eventInFlight || len(pendingEvents) == 0 {
			return
		}
		eventInFlight = true
		event := pendingEvents[0]
		go func() {
			err := postEvent(ctx, client, eventEndpoint, config.Token, event)
			select {
			case eventResults <- err:
			case <-ctx.Done():
			}
		}()
	}
	sendHeartbeat := func() {
		if heartbeatInFlight {
			return
		}
		now := runtime.now()
		report, err := state.prepareReport(config.DeviceID, config.User, sessionState, now)
		if err != nil {
			state.invalidate("report_unavailable")
			log.Printf("report error: %v", err)
			return
		}
		if !persist() {
			return
		}
		heartbeatInFlight = true
		go func() {
			result, err := postHeartbeat(ctx, client, config.Server, config.Token, report)
			select {
			case results <- heartbeatResult{response: result, sentAt: now, err: err, sessionState: report.SessionState}:
			case <-ctx.Done():
			}
		}()
	}
	enforce := func(now time.Time) {
		guard.poll(!state.allowed(now) && sessionState != "locked", now, runtime.lock, func() {
			log.Printf("test_mode=true would_lock=true action=%s reason=%s remaining_seconds=%d", state.Action, state.Reason, state.RemainingSeconds)
		})
	}
	poll := func(now time.Time) {
		if now.Sub(lastPoll) > 5*time.Second || now.Before(lastPoll) {
			flushFocus(lastPoll)
		}
		lastPoll = now
		currentState := runtime.sessionState(runtime.idleTimeout)
		seconds := meter.update(currentState != "locked", now)
		state.account(seconds, now)
		if currentState != "active" {
			flushFocus(now)
		} else if runtime.trackApps {
			if app, err := runtime.foregroundApp(); err == nil {
				if previousApp, activeSeconds, changed := tracker.observe(app, now); changed {
					queueEvent(previousApp, activeSeconds, now)
				}
			}
		}
		if currentState != sessionState {
			log.Printf("session_state=%s", currentState)
			sessionState = currentState
			sendHeartbeat()
		}
		persist()
		if warning, _ := warnings.observe(state.PolicyVersion, state.warningRemaining(now)); warning != nil && state.Action == "allow" && sessionState == "active" {
			if err := runtime.showWarning(*warning); err != nil {
				log.Printf("warning error: %v", err)
			}
		}
		enforce(now)
	}
	// Network operations run separately so a stalled connection cannot stop
	// local enforcement. The first heartbeat establishes server-side presence.
	sessionState = runtime.sessionState(runtime.idleTimeout)
	meter.update(sessionState != "locked", runtime.now())
	sendHeartbeat()
	for {
		select {
		case <-ctx.Done():
			now := runtime.now()
			state.account(meter.update(false, now), now)
			persist()
			return
		case <-runtime.pollTicks:
			poll(runtime.now())
		case <-runtime.reportTicks:
			if app, seconds, ok := tracker.checkpoint(runtime.now()); ok {
				queueEvent(app, seconds, runtime.now())
			}
			sendHeartbeat()
			sendEvent()
		case err := <-eventResults:
			eventInFlight = false
			if err == nil && len(pendingEvents) > 0 {
				pendingEvents = pendingEvents[1:]
				sendEvent()
			}
		case result := <-results:
			heartbeatInFlight = false
			now := runtime.now()
			if result.err != nil {
				if authenticationFailed(result.err) {
					state.invalidate("authentication_failed")
				} else {
					// No usable server decision is available. Fail open so a
					// startup/network outage cannot lock the user out.
					state.markServerUnavailable()
				}
				if lastStatus != "unreachable" {
					log.Printf("server unavailable: %v", result.err)
					lastStatus = "unreachable"
				}
			} else {
				if !guard.enabled {
					log.Printf("test_mode=true heartbeat_ok=true")
				}
				if state.PolicyDate != result.response.PolicyDate {
					warnings = warningState{}
				}
				state.apply(result.response, result.sentAt, now)
				status := state.Action + ":" + state.Reason
				if status != lastStatus {
					log.Printf("server_action=%s reason=%s remaining_seconds=%d", state.Action, state.Reason, state.RemainingSeconds)
					lastStatus = status
				}
				if warning, changed := warnings.observe(state.PolicyVersion, state.warningRemaining(now)); warning != nil && state.Action == "allow" && sessionState == "active" {
					if err := runtime.showWarning(*warning); err != nil {
						log.Printf("warning error: %v", err)
					}
				} else if changed {
					log.Printf("policy_version=%d changed=true", state.PolicyVersion)
				}
			}
			persist()
			enforce(now)
			if result.sessionState != sessionState {
				sendHeartbeat()
			}
		}
	}
}
