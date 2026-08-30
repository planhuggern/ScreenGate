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
	DeviceID      string `json:"device_id"`
	User          string `json:"user"`
	ActiveSeconds int    `json:"active_seconds"`
}

type response struct {
	Action       string `json:"action"`
	Message      string `json:"message"`
	TotalSeconds int    `json:"total_seconds"`
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
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_totals (
		user TEXT PRIMARY KEY,
		total_seconds INTEGER NOT NULL
	)`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (a *application) addToTotal(user string, activeSeconds int) (int, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO user_totals (user, total_seconds) VALUES (?, ?)
		ON CONFLICT(user) DO UPDATE SET total_seconds = total_seconds + excluded.total_seconds`, user, activeSeconds); err != nil {
		return 0, err
	}
	var total int
	if err := tx.QueryRow("SELECT total_seconds FROM user_totals WHERE user = ?", user).Scan(&total); err != nil {
		return 0, err
	}
	return total, tx.Commit()
}

func (a *application) heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var h heartbeat
	var total int
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		log.Printf("invalid heartbeat: %v", err)
	} else if h.DeviceID == "" || h.User == "" || h.ActiveSeconds < 0 {
		log.Printf("invalid heartbeat: device_id and user must not be empty, and active_seconds must not be negative")
	} else {
		var err error
		total, err = a.addToTotal(h.User, h.ActiveSeconds)
		if err != nil {
			log.Printf("database error: %v", err)
		} else {
			log.Printf("device_id=%s user=%s active_seconds=%d total_seconds=%d", h.DeviceID, h.User, h.ActiveSeconds, total)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{Action: "allow", Message: "ok", TotalSeconds: total})
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
