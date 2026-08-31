//go:build windows

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
	Action string `json:"action"`
}

var lockWorkStation = syscall.NewLazyDLL("user32.dll").NewProc("LockWorkStation")

func postHeartbeat(client *http.Client, endpoint, deviceID, username string, activeSeconds int) (string, error) {
	body, err := json.Marshal(heartbeat{DeviceID: deviceID, User: username, ActiveSeconds: activeSeconds, ReportedAt: time.Now()})
	if err != nil {
		return "", err
	}

	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	httpResponse, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer httpResponse.Body.Close()

	var result response
	if err := json.NewDecoder(httpResponse.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Action != "allow" && result.Action != "lock" {
		return "", errors.New("unknown server action")
	}
	return result.Action, nil
}

func main() {
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
	report := time.NewTicker(60 * time.Second)
	defer report.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	lastStatus := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-report.C:
			action, err := postHeartbeat(client, *endpoint, deviceID, currentUser.Username, 60)
			if err != nil {
				if lastStatus != "unreachable" {
					log.Printf("server unavailable: %v", err)
					lastStatus = "unreachable"
				}
				continue
			}
			if action != lastStatus {
				log.Printf("server action=%s", action)
				lastStatus = action
			}
			if action == "lock" {
				lockWorkStation.Call()
			}
		}
	}
}
