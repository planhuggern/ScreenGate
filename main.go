package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type heartbeat struct {
	DeviceID      string `json:"device_id"`
	User          string `json:"user"`
	ActiveSeconds int    `json:"active_seconds"`
}

type response struct {
	Action  string `json:"action"`
	Message string `json:"message"`
}

type application struct {
	mu     sync.Mutex
	totals map[string]int
}

func newApplication() *application {
	return &application{totals: make(map[string]int)}
}

func (a *application) heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var h heartbeat
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		log.Printf("invalid heartbeat: %v", err)
	} else if h.DeviceID == "" || h.User == "" || h.ActiveSeconds < 0 {
		log.Printf("invalid heartbeat: device_id and user must not be empty, and active_seconds must not be negative")
	} else {
		a.mu.Lock()
		a.totals[h.User] += h.ActiveSeconds
		total := a.totals[h.User]
		a.mu.Unlock()
		log.Printf("device_id=%s user=%s active_seconds=%d total_seconds=%d", h.DeviceID, h.User, h.ActiveSeconds, total)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{Action: "allow", Message: "ok"})
}

func main() {
	app := newApplication()
	mux := http.NewServeMux()
	mux.HandleFunc("/heartbeat", app.heartbeatHandler)

	server := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		log.Printf("listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	<-signals

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}
