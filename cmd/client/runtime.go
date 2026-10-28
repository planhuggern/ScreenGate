package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"
)

// The functions and timers supplied by Windows startup. Tests supply substitutes
// so they can run the same loop without locking a real Windows session.
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

// Only the event loop changes these fields. Network workers return results
// through channels; they never update quota, session state or enforcement.
type clientLoop struct {
	ctx     context.Context
	config  clientConfig
	runtime clientRuntime
	state   clientState
	guard   enforcement

	sessionState string
	lastPoll     time.Time
	meter        activityMeter
	warnings     warningState
	lastStatus   string

	heartbeatInFlight bool
	heartbeatResults  chan heartbeatResult
	usageSyncUntil    time.Time

	// Optional foreground-app reports are independent of quota accounting.
	tracker       focusTracker
	pendingEvents []focusEvent
	eventInFlight bool
	eventResults  chan error
}

func runClient(ctx context.Context, config clientConfig, state clientState, guard enforcement, runtime clientRuntime) {
	client := clientLoop{
		ctx: ctx, config: config, runtime: runtime, state: state, guard: guard,
		usageSyncUntil:   state.LastUsageEnd,
		heartbeatResults: make(chan heartbeatResult, 1),
		eventResults:     make(chan error, 1),
	}
	client.persist()
	client.lastPoll = runtime.now()
	client.sessionState = runtime.sessionState(runtime.idleTimeout)
	client.meter.update(client.sessionState != "locked", runtime.now())
	client.sendHeartbeat()

	for {
		select {
		case <-ctx.Done():
			client.stop()
			return
		case <-runtime.pollTicks:
			client.poll(runtime.now())
		case <-runtime.reportTicks:
			client.report()
		case result := <-client.heartbeatResults:
			client.handleHeartbeat(result)
		case err := <-client.eventResults:
			client.handleEvent(err)
		}
	}
}

// poll observes the session, accounts local use and checks access in that order.
func (c *clientLoop) poll(now time.Time) {
	if now.Sub(c.lastPoll) > 5*time.Second || now.Before(c.lastPoll) {
		c.flushFocus(c.lastPoll)
	}
	c.lastPoll = now

	currentState := c.runtime.sessionState(c.runtime.idleTimeout)
	seconds := c.meter.update(currentState != "locked", now)
	previousDate := c.state.PolicyDate
	c.state.account(seconds, now)
	if c.state.PolicyDate != previousDate {
		c.warnings = warningState{}
	}

	if currentState != "active" {
		c.flushFocus(now)
	} else if c.runtime.trackApps {
		if app, err := c.runtime.foregroundApp(); err == nil {
			if previousApp, activeSeconds, changed := c.tracker.observe(app, now); changed {
				c.queueEvent(previousApp, activeSeconds, now)
			}
		}
	}

	if currentState != c.sessionState {
		log.Printf("session_state=%s", currentState)
		c.sessionState = currentState
		c.sendHeartbeat()
	}
	c.persist()

	if warning, _ := c.warnings.observe(c.state.PolicyVersion, c.state.warningRemaining(now)); warning != nil && c.state.Action == "allow" && c.sessionState == "active" {
		if err := c.runtime.showWarning(*warning); err != nil {
			log.Printf("warning error: %v", err)
		}
	}
	c.enforce(now)
}

func (c *clientLoop) report() {
	if app, seconds, ok := c.tracker.checkpoint(c.runtime.now()); ok {
		c.queueEvent(app, seconds, c.runtime.now())
	}
	c.sendHeartbeat()
	c.sendEvent()
}

func (c *clientLoop) stop() {
	now := c.runtime.now()
	c.state.account(c.meter.update(false, now), now)
	c.persist()
}

func (c *clientLoop) persist() bool {
	if err := c.runtime.save(c.state); err != nil {
		c.state.invalidate("state_unavailable")
		log.Printf("cannot persist authorization: %v", err)
		return false
	}
	return true
}

func (c *clientLoop) enforce(now time.Time) {
	blocked := !c.state.allowed(now) && c.sessionState != "locked"
	c.guard.poll(blocked, now, c.runtime.lock, func() {
		log.Printf("test_mode=true would_lock=true action=%s reason=%s remaining_seconds=%d", c.state.Action, c.state.Reason, c.state.RemainingSeconds)
	})
}

type heartbeatResult struct {
	sessionState string
	response     response
	sentAt       time.Time
	err          error
}

// Save the report before sending. A network worker cannot block local checks.
func (c *clientLoop) sendHeartbeat() {
	if c.heartbeatInFlight {
		return
	}
	now := c.runtime.now()
	report, err := c.state.prepareReport(c.config.DeviceID, c.config.User, c.sessionState, now)
	if err != nil {
		c.state.invalidate("report_unavailable")
		log.Printf("report error: %v", err)
		return
	}
	if !c.persist() {
		return
	}
	c.heartbeatInFlight = true
	go func() {
		result, err := postHeartbeat(c.ctx, c.runtime.httpClient, c.config.Server, c.config.Token, report)
		select {
		case c.heartbeatResults <- heartbeatResult{response: result, sentAt: now, err: err, sessionState: report.SessionState}:
		case <-c.ctx.Done():
		}
	}()
}

func (c *clientLoop) handleHeartbeat(result heartbeatResult) {
	c.heartbeatInFlight = false
	now := c.runtime.now()
	recovering := c.state.ServerUnavailable
	if result.err != nil {
		c.handleHeartbeatError(result.err)
	} else {
		c.applyServerResponse(result, now)
		if recovering {
			c.usageSyncUntil = c.state.LastUsageEnd
		}
	}
	c.persist()
	c.enforce(now)

	backlog := len(c.state.UnreportedUsage) > 0 && c.state.UnreportedUsage[0].Start.Before(c.usageSyncUntil)
	if result.sessionState != c.sessionState || (result.err == nil && backlog) {
		c.sendHeartbeat()
	}
}

func (c *clientLoop) handleHeartbeatError(err error) {
	if authenticationFailed(err) {
		c.state.ServerUnavailable = false
		c.state.invalidate("authentication_failed")
	} else {
		// Keep the last decision. Local quota and the known closing time still
		// apply while the server cannot supply an update.
		c.state.markServerUnavailable()
	}
	if c.lastStatus != "unreachable" {
		log.Printf("server unavailable: %v", err)
		c.lastStatus = "unreachable"
	}
}

func (c *clientLoop) applyServerResponse(result heartbeatResult, now time.Time) {
	if !c.guard.enabled {
		log.Printf("test_mode=true heartbeat_ok=true")
	}
	if c.state.PolicyDate != result.response.PolicyDate {
		c.warnings = warningState{}
	}
	c.state.apply(result.response, result.sentAt, now)

	status := c.state.Action + ":" + c.state.Reason
	if status != c.lastStatus {
		log.Printf("server_action=%s reason=%s remaining_seconds=%d", c.state.Action, c.state.Reason, c.state.RemainingSeconds)
		c.lastStatus = status
	}
	if warning, changed := c.warnings.observe(c.state.PolicyVersion, c.state.warningRemaining(now)); warning != nil && c.state.Action == "allow" && c.sessionState == "active" {
		if err := c.runtime.showWarning(*warning); err != nil {
			log.Printf("warning error: %v", err)
		}
	} else if changed {
		log.Printf("policy_version=%d changed=true", c.state.PolicyVersion)
	}
}

// The remaining methods handle optional app tracking. They do not change quota.
func newFocusEvent(deviceID, username, app string, activeSeconds int, timestamp time.Time) focusEvent {
	return focusEvent{Type: "focus_changed", DeviceID: deviceID, User: username, PreviousApp: app, ActiveSeconds: activeSeconds, Timestamp: timestamp}
}

func (c *clientLoop) queueEvent(app string, seconds int, now time.Time) {
	if app == "" || seconds <= 0 || !c.runtime.trackApps {
		return
	}
	if len(c.pendingEvents) >= 256 {
		return
	}
	c.pendingEvents = append(c.pendingEvents, newFocusEvent(c.config.DeviceID, c.config.User, app, min(seconds, 86400), now))
}

func (c *clientLoop) flushFocus(now time.Time) {
	if app, seconds, ok := c.tracker.finish(now); ok {
		c.queueEvent(app, seconds, now)
	}
}

func (c *clientLoop) sendEvent() {
	if c.eventInFlight || len(c.pendingEvents) == 0 {
		return
	}
	c.eventInFlight = true
	event := c.pendingEvents[0]
	endpoint := strings.TrimSuffix(c.config.Server, "/heartbeat") + "/event"
	go func() {
		err := postEvent(c.ctx, c.runtime.httpClient, endpoint, c.config.Token, event)
		select {
		case c.eventResults <- err:
		case <-c.ctx.Done():
		}
	}()
}

func (c *clientLoop) handleEvent(err error) {
	c.eventInFlight = false
	if err == nil && len(c.pendingEvents) > 0 {
		c.pendingEvents = c.pendingEvents[1:]
		c.sendEvent()
	}
}
