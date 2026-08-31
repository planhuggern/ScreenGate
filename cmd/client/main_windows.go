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
	"path/filepath"
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
	defer report.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	lastStatus := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-report.C:
			action, err := postHeartbeat(client, *endpoint, deviceID, currentUser.Username, 30)
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
