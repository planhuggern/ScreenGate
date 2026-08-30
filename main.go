package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

type heartbeat struct {
	DeviceID      string    `json:"device_id"`
	User          string    `json:"user"`
	ActiveSeconds int       `json:"active_seconds"`
	ReportedAt    time.Time `json:"reported_at"`
}

type response struct {
	Action            string `json:"action"`
	Message           string `json:"message"`
	DailyTotalSeconds int    `json:"daily_total_seconds"`
}

type application struct {
	db *sql.DB
}

func newApplication(db *sql.DB) *application {
	return &application{db: db}
}

func openDatabase(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS heartbeats (
		id INTEGER PRIMARY KEY,
		reported_at TEXT NOT NULL,
		date TEXT NOT NULL,
		device_id TEXT NOT NULL,
		user TEXT NOT NULL,
		active_seconds INTEGER NOT NULL
	)`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS daily_totals (
		user TEXT NOT NULL,
		date TEXT NOT NULL,
		total_seconds INTEGER NOT NULL,
		PRIMARY KEY (user, date)
	)`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (a *application) addHeartbeat(h heartbeat) (int, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	date := h.ReportedAt.Format("2006-01-02")
	if _, err := tx.Exec(`INSERT INTO heartbeats (reported_at, date, device_id, user, active_seconds)
		VALUES (?, ?, ?, ?, ?)`, h.ReportedAt.Format(time.RFC3339Nano), date, h.DeviceID, h.User, h.ActiveSeconds); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO daily_totals (user, date, total_seconds) VALUES (?, ?, ?)
		ON CONFLICT(user, date) DO UPDATE SET total_seconds = total_seconds + excluded.total_seconds`, h.User, date, h.ActiveSeconds); err != nil {
		return 0, err
	}
	var dailyTotal int
	if err := tx.QueryRow("SELECT total_seconds FROM daily_totals WHERE user = ? AND date = ?", h.User, date).Scan(&dailyTotal); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return dailyTotal, nil
}

func (a *application) heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var h heartbeat
	var dailyTotal int
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		log.Printf("invalid heartbeat: %v", err)
	} else if h.DeviceID == "" || h.User == "" || h.ActiveSeconds < 0 || h.ReportedAt.IsZero() {
		log.Printf("invalid heartbeat: device_id, user and reported_at must not be empty, and active_seconds must not be negative")
	} else {
		var err error
		dailyTotal, err = a.addHeartbeat(h)
		if err != nil {
			log.Printf("database error: %v", err)
		} else {
			log.Printf("reported_at=%s device_id=%s user=%s active_seconds=%d daily_total_seconds=%d", h.ReportedAt.Format(time.RFC3339), h.DeviceID, h.User, h.ActiveSeconds, dailyTotal)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{Action: "allow", Message: "ok", DailyTotalSeconds: dailyTotal})
}

func main() {
	databasePath := os.Getenv("DATABASE_PATH")
	if databasePath == "" {
		databasePath = "screengate.db"
	}
	db, err := openDatabase(databasePath)
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer db.Close()

	app := newApplication(db)
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
