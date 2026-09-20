//go:build windows

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var lockWorkStation = syscall.NewLazyDLL("user32.dll").NewProc("LockWorkStation")

func clientDataDir() string {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		root, _ = os.UserConfigDir()
	}
	return filepath.Join(root, "ScreenGate")
}

func configureLogging() {
	logDir := clientDataDir()
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return
	}
	logPath := filepath.Join(logDir, "client.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() >= 1<<20 {
		os.Remove(logPath + ".1")
		os.Rename(logPath, logPath+".1")
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		log.SetOutput(file)
	}
}

func newFocusEvent(deviceID, username, app string, activeSeconds int, timestamp time.Time) focusEvent {
	return focusEvent{Type: "focus_changed", DeviceID: deviceID, User: username, PreviousApp: app, ActiveSeconds: activeSeconds, Timestamp: timestamp}
}

type heartbeatResult struct {
	response response
	sentAt   time.Time
	err      error
}

func main() {
	configureLogging()
	configPath := flag.String("config", "", "path to protected installation configuration")
	testMode := flag.Bool("test-mode", false, "force safe test mode: never lock Windows")
	enableLocking := flag.Bool("enable-locking", false, "explicitly enable Windows locking")
	endpoint := flag.String("server", "", "ScreenGate heartbeat URL (http(s)://host:port/heartbeat)")
	token := flag.String("token", "", "device token; prefer -token-file or protected -config")
	tokenPath := flag.String("token-file", "", "file containing the device token")
	userFlag := flag.String("user", "", "logical ScreenGate user assigned during pairing")
	deviceFlag := flag.String("device-id", "", "device ID assigned during pairing")
	idleTimeout := flag.Duration("idle-timeout", 0, "optional inactivity cutoff, e.g. 5m; 0 counts passive screen use")
	trackApps := flag.Bool("track-apps", false, "optional foreground application reporting (disabled by default)")
	debugRemaining := flag.String("debug-remaining", "", "comma-separated remaining_seconds values for warning testing")
	flag.Parse()
	if *debugRemaining != "" {
		runWarningDebug(*debugRemaining)
		return
	}
	if *idleTimeout < 0 {
		log.Fatal("idle-timeout cannot be negative")
	}
	config := clientConfig{Token: strings.TrimSpace(os.Getenv("SCREENGATE_DEVICE_TOKEN"))}
	if *configPath != "" {
		var err error
		config, err = readConfig(*configPath)
		if err != nil {
			log.Fatal(err)
		}
	}
	if *endpoint != "" {
		config.Server = *endpoint
	}
	if *token != "" {
		config.Token = strings.TrimSpace(*token)
	}
	if *tokenPath != "" {
		data, err := os.ReadFile(*tokenPath)
		if err != nil {
			log.Fatal("cannot read device token: ", err)
		}
		config.Token = strings.TrimSpace(string(data))
	}
	if *userFlag != "" {
		config.User = *userFlag
	}
	if *deviceFlag != "" {
		config.DeviceID = *deviceFlag
	}
	if config.DeviceID == "" {
		var err error
		config.DeviceID, err = os.Hostname()
		if err != nil {
			log.Fatal(err)
		}
	}
	if config.User == "" {
		currentUser, err := user.Current()
		if err != nil {
			log.Fatal(err)
		}
		config.User = currentUser.Username
	}
	var err error
	config.Server, err = validateEndpoint(config.Server)
	if err != nil {
		log.Fatal(err)
	}
	if config.Token == "" {
		log.Fatal("device token missing; pair this Windows user using install.ps1")
	}
	guard := enforcement{enabled: (config.EnableLocking || *enableLocking) && !*testMode}
	log.Printf("test_mode=%t locking_enabled=%t", !guard.enabled, guard.enabled)
	identity := stateIdentity(config.Server, config.DeviceID, config.User)
	mutex, err := acquireClientMutex(identity)
	if err != nil {
		log.Fatal(err)
	}
	defer closeHandle.Call(mutex)
	statePath := filepath.Join(clientDataDir(), "state-"+identity[:16]+".json")
	state, err := loadState(statePath, identity, config.Token, time.Now())
	if err != nil {
		log.Printf("saved authorization unavailable: %v", err)
	}
	persist := func() bool {
		if err := saveState(statePath, config.Token, state); err != nil {
			state.invalidate("state_unavailable")
			log.Printf("cannot persist authorization: %v", err)
			return false
		}
		return true
	}
	persist()
	client := newHTTPClient()
	eventEndpoint := strings.TrimSuffix(config.Server, "/heartbeat") + "/event"
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	reportTicker := time.NewTicker(30 * time.Second)
	pollTicker := time.NewTicker(time.Second)
	defer reportTicker.Stop()
	defer pollTicker.Stop()
	results := make(chan heartbeatResult, 1)
	eventResults := make(chan error, 1)
	heartbeatInFlight, eventInFlight := false, false
	sessionState := "locked"
	warnings := warningState{}
	tracker := focusTracker{}
	meter := activityMeter{}
	var pendingEvents []focusEvent
	lastStatus := ""
	lastPoll := time.Now()

	queueEvent := func(app string, seconds int, now time.Time) {
		if app == "" || seconds <= 0 || !*trackApps {
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
		now := time.Now()
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
			case results <- heartbeatResult{response: result, sentAt: now, err: err}:
			case <-ctx.Done():
			}
		}()
	}
	enforce := func(now time.Time) {
		guard.poll(!state.allowed(now) && sessionState != "locked", now, func() {
			if ok, _, err := lockWorkStation.Call(); ok == 0 {
				log.Printf("lock workstation failed: %v", err)
			}
		}, func() {
			log.Printf("test_mode=true would_lock=true action=%s reason=%s remaining_seconds=%d", state.Action, state.Reason, state.RemainingSeconds)
		})
	}
	poll := func(now time.Time) {
		if now.Sub(lastPoll) > 5*time.Second || now.Before(lastPoll) {
			flushFocus(lastPoll)
		}
		lastPoll = now
		currentState := currentSessionState(*idleTimeout)
		seconds := meter.update(currentState == "active", now)
		state.account(seconds, now)
		if currentState != "active" {
			flushFocus(now)
		} else if *trackApps {
			if app, err := foregroundApp(); err == nil {
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
			if err := showWarning(*warning); err != nil {
				log.Printf("warning error: %v", err)
			}
		}
		enforce(now)
	}
	// Network operations run separately so a stalled connection cannot stop
	// local enforcement. A fresh installation reports zero seconds initially.
	sessionState = currentSessionState(*idleTimeout)
	meter.update(sessionState == "active", time.Now())
	sendHeartbeat()
	for {
		select {
		case <-ctx.Done():
			now := time.Now()
			state.account(meter.update(false, now), now)
			persist()
			return
		case <-pollTicker.C:
			poll(time.Now())
		case <-reportTicker.C:
			if app, seconds, ok := tracker.checkpoint(time.Now()); ok {
				queueEvent(app, seconds, time.Now())
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
			now := time.Now()
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
					if err := showWarning(*warning); err != nil {
						log.Printf("warning error: %v", err)
					}
				} else if changed {
					log.Printf("policy_version=%d changed=true", state.PolicyVersion)
				}
			}
			persist()
			enforce(now)
			if result.err == nil && state.PendingSeconds > 0 && state.PendingDate != "" && state.PendingDate != state.PolicyDate {
				sendHeartbeat()
			}
		}
	}
}

func runWarningDebug(values string) {
	state := warningState{}
	for _, value := range strings.Split(values, ",") {
		remaining, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			log.Printf("invalid debug remaining value: %s", value)
			continue
		}
		warning, _ := state.observe(1, remaining)
		if warning != nil {
			log.Printf("debug warning=%d remaining_seconds=%d", warning.threshold, remaining)
			if err := showWarning(*warning); err != nil {
				log.Printf("warning error: %v", err)
			}
		}
	}
	time.Sleep(6 * time.Second)
}
