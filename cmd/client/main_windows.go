//go:build windows

// Tolker hver
// heartbeat som blocked=true/false, sender
// heartbeat straks ved session unlock/logon,
// og låser når den ferske beslutningen
// fortsatt er lock.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type heartbeat struct {
	DeviceID      string    `json:"device_id"`
	User          string    `json:"user"`
	ActiveSeconds int       `json:"active_seconds"`
	ReportedAt    time.Time `json:"reported_at"`
}

type response struct {
	Action        string `json:"action"`
	PolicyVersion int    `json:"policy_version"`
}

type focusEvent struct {
	Type          string    `json:"type"`
	DeviceID      string    `json:"device_id"`
	User          string    `json:"user"`
	PreviousApp   string    `json:"previous_app"`
	ActiveSeconds int       `json:"active_seconds"`
	Timestamp     time.Time `json:"timestamp"`
}

var lockWorkStation = syscall.NewLazyDLL("user32.dll").NewProc("LockWorkStation")

func configureLogging() {
	logDir := filepath.Join(os.Getenv("ProgramData"), "ScreenGate")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return
	}
	logPath := filepath.Join(logDir, "client.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() >= 1<<20 {
		os.Remove(logPath + ".1")
		os.Rename(logPath, logPath+".1")
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		log.SetOutput(file)
	}
}

func postHeartbeat(client *http.Client, endpoint, deviceID, username string, activeSeconds int) (response, error) {
	body, err := json.Marshal(heartbeat{DeviceID: deviceID, User: username, ActiveSeconds: activeSeconds, ReportedAt: time.Now()})
	if err != nil {
		return response{}, err
	}

	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return response{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	httpResponse, err := client.Do(request)
	if err != nil {
		return response{}, err
	}
	defer httpResponse.Body.Close()

	var result response
	if err := json.NewDecoder(httpResponse.Body).Decode(&result); err != nil {
		return response{}, err
	}
	if result.Action != "allow" && result.Action != "lock" {
		return response{}, errors.New("unknown server action")
	}
	return result, nil
}

func postEvent(client *http.Client, endpoint string, event focusEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("event request failed")
	}
	return nil
}

func flushEvents(client *http.Client, endpoint string, events []focusEvent) []focusEvent {
	for len(events) > 0 {
		if err := postEvent(client, endpoint, events[0]); err != nil {
			return events
		}
		events = events[1:]
	}
	return events
}

func newFocusEvent(deviceID, username, app string, activeSeconds int, timestamp time.Time) focusEvent {
	return focusEvent{
		Type:          "focus_changed",
		DeviceID:      deviceID,
		User:          username,
		PreviousApp:   app,
		ActiveSeconds: activeSeconds,
		Timestamp:     timestamp,
	}
}

func main() {
	configureLogging()
	endpoint := flag.String("server", "http://10.0.0.20:8081/heartbeat", "ScreenGate heartbeat URL")
	flag.Parse()

	deviceID, err := os.Hostname()
	if err != nil {
		return
	}
	currentUser, err := user.Current()
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	report := time.NewTicker(30 * time.Second)
	focusPoll := time.NewTicker(time.Second)
	defer report.Stop()
	defer focusPoll.Stop()
	eventEndpoint := strings.TrimSuffix(*endpoint, "/heartbeat") + "/event"

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	lastStatus := ""
	state := blockedState{}
	tracker := focusTracker{}
	pendingEvents := []focusEvent{}
	sessionEvents := startSessionEvents()

	applyHeartbeat := func() (string, error) {
		result, err := postHeartbeat(client, *endpoint, deviceID, currentUser.Username, 30)
		if err != nil {
			return "", err
		}
		state.applyServerAction(result.Action)
		if state.updatePolicyVersion(result.PolicyVersion) {
			log.Printf("policy_version=%d changed=true", result.PolicyVersion)
		}
		return result.Action, nil
	}

	if action, err := applyHeartbeat(); err != nil {
		log.Printf("server unavailable: %v", err)
		lastStatus = "unreachable"
	} else {
		log.Printf("server_action=%s blocked=%t", action, state.blocked)
		lastStatus = action
		if action == "lock" {
			lockWorkStation.Call()
		}
	}

	for {
		select {
		case <-ctx.Done():
			if app, activeSeconds, ok := tracker.finish(time.Now()); ok {
				pendingEvents = append(pendingEvents, newFocusEvent(deviceID, currentUser.Username, app, activeSeconds, time.Now()))
			}
			flushEvents(client, eventEndpoint, pendingEvents)
			return
		case <-focusPoll.C:
			app, err := foregroundApp()
			if err != nil {
				continue
			}
			now := time.Now()
			if previousApp, activeSeconds, changed := tracker.observe(app, now); changed {
				pendingEvents = append(pendingEvents, newFocusEvent(deviceID, currentUser.Username, previousApp, activeSeconds, now))
				pendingEvents = flushEvents(client, eventEndpoint, pendingEvents)
			}
		case sessionEvent, ok := <-sessionEvents:
			if !ok {
				sessionEvents = nil
				continue
			}
			wasBlocked := state.blocked
			action, err := applyHeartbeat()
			if err != nil {
				log.Printf("session_event=%s heartbeat_error=%v blocked=%t", sessionEvent, err, state.blocked)
				if wasBlocked && state.lockOnSessionEventWhenHeartbeatFails() {
					lockWorkStation.Call()
				}
				continue
			}
			log.Printf("session_event=%s heartbeat_action=%s blocked=%t", sessionEvent, action, state.blocked)
			lastStatus = action
			if action == "lock" {
				lockWorkStation.Call()
			}
		case <-report.C:
			action, err := applyHeartbeat()
			if err != nil {
				if lastStatus != "unreachable" {
					log.Printf("server unavailable: %v", err)
					lastStatus = "unreachable"
				}
				continue
			}
			pendingEvents = flushEvents(client, eventEndpoint, pendingEvents)
			if action != lastStatus {
				log.Printf("server_action=%s blocked=%t", action, state.blocked)
				lastStatus = action
			}
			if action == "lock" {
				lockWorkStation.Call()
			}
		}
	}
}
