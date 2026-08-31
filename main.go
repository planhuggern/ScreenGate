package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"html/template"
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

type activity struct {
	DeviceID       string
	User           string
	TotalSeconds   int
	LastReportedAt string
	Action         string
}

type dashboard struct {
	Date       string
	Activities []activity
}

var dashboardTemplate = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"duration": func(seconds int) string {
		return (time.Duration(seconds) * time.Second).String()
	},
}).Parse(`<!doctype html>
<html lang="no">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ScreenGate</title>
  <style>body{font-family:system-ui,sans-serif;max-width:820px;margin:3rem auto;padding:0 1rem}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:.6rem;border-bottom:1px solid #ddd}.switch{position:relative;display:inline-block;width:52px;height:28px}.switch input{opacity:0;width:0;height:0}.slider{position:absolute;inset:0;background:#22a06b;border-radius:28px;cursor:pointer;transition:.2s}.slider:before{content:"";position:absolute;height:22px;width:22px;left:3px;bottom:3px;background:white;border-radius:50%;transition:.2s}.switch input:checked+.slider{background:#d14343}.switch input:checked+.slider:before{transform:translateX(24px)}.action-label{font-size:.85rem;margin-left:.35rem}</style>
</head>
<body>
  <h1>ScreenGate</h1>
  <p><a href="/downloads/install.ps1"><button>Last ned installasjon for Windows</button></a></p>
  <p>Kjør deretter i PowerShell som administrator:</p>
  <code>powershell -ExecutionPolicy Bypass -File .\install.ps1</code>
  <p>Skriptet viser en liste over Windows-brukere. Velg brukeren som skal kjøre klienten.</p>
  <h3>Avinstaller</h3>
  <p>Kjør dette i PowerShell som administrator for å fjerne oppstartsoppgaven og klientfilene:</p>
  <code>Unregister-ScheduledTask -TaskName "ScreenGate Client" -Confirm:$false; Remove-Item "C:\Program Files\ScreenGate" -Recurse -Force</code>
  <h2>Aktivitet {{.Date}}</h2>
  {{if .Activities}}
  <table>
    <tr><th>PC</th><th>Bruker</th><th>Brukt i dag</th><th>Sist rapportert</th><th>Handling</th></tr>
    {{range .Activities}}<tr><td>{{.DeviceID}}</td><td>{{.User}}</td><td>{{duration .TotalSeconds}}</td><td>{{.LastReportedAt}}</td><td>
      <form method="post" action="/device-action"><input type="hidden" name="device_id" value="{{.DeviceID}}"><input type="hidden" name="action" value="{{.Action}}"><label class="switch"><input type="checkbox" {{if eq .Action "lock"}}checked{{end}} onchange="this.form.querySelector('input[name=action]').value=this.checked?'lock':'allow';this.form.submit()"><span class="slider"></span></label><span class="action-label">{{if eq .Action "lock"}}Lås{{else}}Tillat{{end}}</span><noscript><button type="submit">Lagre</button></noscript></form>
    </td></tr>{{end}}
  </table>
  {{else}}<p>Ingen aktivitet registrert i dag.</p>{{end}}
</body>
</html>`))

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
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS device_actions (
		device_id TEXT PRIMARY KEY,
		action TEXT NOT NULL,
		action_date TEXT NOT NULL
	)`); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func today() string {
	return time.Now().Format("2006-01-02")
}

func (a *application) deviceAction(deviceID string) (string, error) {
	var action string
	err := a.db.QueryRow("SELECT action FROM device_actions WHERE device_id = ? AND action_date = ?", deviceID, today()).Scan(&action)
	if err == sql.ErrNoRows {
		return "allow", nil
	}
	return action, err
}

func (a *application) setDeviceAction(deviceID, action string) error {
	_, err := a.db.Exec(`INSERT INTO device_actions (device_id, action, action_date) VALUES (?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET action = excluded.action, action_date = excluded.action_date`, deviceID, action, today())
	return err
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

func (a *application) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	date := today()
	rows, err := a.db.Query(`SELECT h.device_id, h.user, d.total_seconds, MAX(h.reported_at), COALESCE(a.action, 'allow')
		FROM heartbeats h
		JOIN daily_totals d ON d.user = h.user AND d.date = h.date
		LEFT JOIN device_actions a ON a.device_id = h.device_id AND a.action_date = ?
		WHERE d.date = ?
		GROUP BY h.device_id, h.user, d.total_seconds, a.action
		ORDER BY h.device_id`, date, date)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var activities []activity
	for rows.Next() {
		var item activity
		if err := rows.Scan(&item.DeviceID, &item.User, &item.TotalSeconds, &item.LastReportedAt, &item.Action); err != nil {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		activities = append(activities, item)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashboardTemplate.Execute(w, dashboard{Date: date, Activities: activities}); err != nil {
		log.Printf("dashboard error: %v", err)
	}
}

func (a *application) deviceActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	deviceID := r.FormValue("device_id")
	action := r.FormValue("action")
	if deviceID == "" || (action != "allow" && action != "lock") {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	if err := a.setDeviceAction(deviceID, action); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func downloadClientHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.microsoft.portable-executable")
	w.Header().Set("Content-Disposition", "attachment; filename=screengate-client.exe")
	http.ServeFile(w, r, "/client/screengate-client.exe")
}

func downloadInstallerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=install.ps1")
	http.ServeFile(w, r, "/client/install.ps1")
}

func (a *application) heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var h heartbeat
	var dailyTotal int
	action := "allow"
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
			action, err = a.deviceAction(h.DeviceID)
			if err != nil {
				log.Printf("database error: %v", err)
				action = "allow"
			}
			log.Printf("reported_at=%s device_id=%s user=%s active_seconds=%d daily_total_seconds=%d", h.ReportedAt.Format(time.RFC3339), h.DeviceID, h.User, h.ActiveSeconds, dailyTotal)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{Action: action, Message: "ok", DailyTotalSeconds: dailyTotal})
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
	mux.HandleFunc("/downloads/install.ps1", downloadInstallerHandler)
	mux.HandleFunc("/downloads/screengate-client.exe", downloadClientHandler)
	mux.HandleFunc("/device-action", app.deviceActionHandler)
	mux.HandleFunc("/", app.dashboardHandler)
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
