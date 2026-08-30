//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"syscall"
	"time"
)

type heartbeat struct {
	DeviceID      string `json:"device_id"`
	User          string `json:"user"`
	ActiveSeconds int    `json:"active_seconds"`
}

func postHeartbeat(client *http.Client, endpoint, deviceID, username string, activeSeconds int) {
	body, err := json.Marshal(heartbeat{deviceID, username, activeSeconds})
	if err != nil {
		return
	}

	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err == nil {
		response.Body.Close()
	}
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-report.C:
			postHeartbeat(client, *endpoint, deviceID, currentUser.Username, 60)
		}
	}
}
