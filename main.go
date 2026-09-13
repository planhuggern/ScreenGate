package main

import (
	"context"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

type response struct {
	Action            string `json:"action"`
	Message           string `json:"message"`
	DailyTotalSeconds int    `json:"daily_total_seconds"`
	PolicyVersion     int    `json:"policy_version"`
	RemainingSeconds  int    `json:"remaining_seconds"`
}

type focusEvent struct {
	Type          string    `json:"type"`
	DeviceID      string    `json:"device_id"`
	User          string    `json:"user"`
	PreviousApp   string    `json:"previous_app"`
	ActiveSeconds int       `json:"active_seconds"`
	Timestamp     time.Time `json:"timestamp"`
}

type application struct {
	repository *repository
	adminPath  string
}

type dashboard struct {
	Date       string
	Activities []activity
	AdminPath  string
}

var dashboardTemplate = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"duration": func(seconds int) string {
		return (time.Duration(seconds) * time.Second).String()
	},
	"hours": func(seconds int) int {
		return seconds / 3600
	},
	"minutes": func(seconds int) int {
		return seconds % 3600 / 60
	},
	"quota": func(seconds int) string {
		if seconds == 0 {
			return "Ubegrenset"
		}
		return (time.Duration(seconds) * time.Second).String()
	},
}).Parse(`<!doctype html>
<html lang="no">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ScreenGate</title>
  <style>body{font-family:system-ui,sans-serif;max-width:820px;margin:3rem auto;padding:0 1rem}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:.6rem;border-bottom:1px solid #ddd}input{width:4rem}</style>
</head>
<body>
  <h1>ScreenGate</h1>
  <p><a href="{{.AdminPath}}/downloads/install.ps1"><button>Last ned installasjon for Windows</button></a></p>
  <p>Kjør deretter i PowerShell som administrator:</p>
  <code>powershell -ExecutionPolicy Bypass -File .\install.ps1</code>
  <p>Skriptet viser en liste over Windows-brukere. Velg brukeren som skal kjøre klienten.</p>
  <h3>Avinstaller</h3>
  <p>Kjør dette i PowerShell som administrator for å fjerne oppstartsoppgaven og klientfilene:</p>
  <code>Unregister-ScheduledTask -TaskName "ScreenGate Client" -Confirm:$false; Remove-Item "C:\Program Files\ScreenGate" -Recurse -Force</code>
  <h2>Aktivitet {{.Date}}</h2>
  {{if .Activities}}
  <table>
    <tr><th>Bruker</th><th>Brukt i dag</th><th>Maks per dag</th><th>Sist rapportert</th></tr>
    {{range .Activities}}<tr><td>{{.User}}</td><td>{{duration .TotalSeconds}}</td><td>
      <form method="post" action="{{$.AdminPath}}/user-quota"><input type="hidden" name="user" value="{{.User}}"><input type="number" name="hours" min="0" value="{{hours .QuotaSeconds}}"> t <input type="number" name="minutes" min="0" max="59" value="{{minutes .QuotaSeconds}}"> min <button>Lagre</button><br><small>{{quota .QuotaSeconds}}</small></form>
    </td><td>{{.LastReportedAt}}</td></tr>{{end}}
  </table>
  {{else}}<p>Ingen aktivitet registrert i dag.</p>{{end}}
</body>
</html>`))

var overviewTemplate = template.Must(template.New("overview").Funcs(template.FuncMap{
	"duration": func(seconds int) string {
		return (time.Duration(seconds) * time.Second).String()
	},
	"quota": func(seconds int) string {
		if seconds == 0 {
			return "Ubegrenset"
		}
		return (time.Duration(seconds) * time.Second).String()
	},
}).Parse(`<!doctype html>
<html lang="no">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>ScreenGate</title>
  <style>body{font-family:system-ui,sans-serif;max-width:760px;margin:3rem auto;padding:0 1rem}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:.6rem;border-bottom:1px solid #ddd}</style>
</head>
<body>
  <h1>ScreenGate</h1>
  <h2>Aktivitet {{.Date}}</h2>
  {{if .Activities}}
  <table>
    <tr><th>Bruker</th><th>Brukt i dag</th><th>Maks per dag</th><th>Sist rapportert</th></tr>
    {{range .Activities}}<tr><td>{{.User}}</td><td>{{duration .TotalSeconds}}</td><td>{{quota .QuotaSeconds}}</td><td>{{.LastReportedAt}}</td></tr>{{end}}
  </table>
  {{else}}<p>Ingen aktivitet registrert i dag.</p>{{end}}
</body>
</html>`))

func newApplication(repository *repository) *application {
	return &application{repository: repository, adminPath: "/admin"}
}

func today() string {
	return time.Now().Format("2006-01-02")
}

func (a *application) addHeartbeat(h heartbeat) (int, error) {
	date := h.ReportedAt.In(time.Local).Format("2006-01-02")
	if err := a.repository.addHeartbeat(h); err != nil {
		return 0, err
	}
	return a.dailyTotal(h.User, date)
}

func (a *application) dailyTotal(user, date string) (int, error) {
	heartbeats, err := a.repository.heartbeatsForUser(user)
	if err != nil {
		return 0, err
	}

	return calculateDailyTotal(heartbeats, date)
}

func (a *application) todaysActivities() ([]activity, error) {
	date := today()
	activities, err := a.repository.activities()
	if err != nil {
		return nil, err
	}
	for i := range activities {
		item := &activities[i]
		item.TotalSeconds, err = a.dailyTotal(item.User, date)
		if err != nil {
			return nil, err
		}
	}
	return activities, nil
}

func (a *application) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != a.adminPath {
		http.NotFound(w, r)
		return
	}

	activities, err := a.todaysActivities()
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashboardTemplate.Execute(w, dashboard{Date: today(), Activities: activities, AdminPath: a.adminPath}); err != nil {
		log.Printf("dashboard error: %v", err)
	}
}

func (a *application) overviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	activities, err := a.todaysActivities()
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := overviewTemplate.Execute(w, dashboard{Date: today(), Activities: activities}); err != nil {
		log.Printf("overview error: %v", err)
	}
}

func (a *application) userQuotaHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	user := r.FormValue("user")
	hours, hoursErr := strconv.Atoi(r.FormValue("hours"))
	minutes, minutesErr := strconv.Atoi(r.FormValue("minutes"))
	if user == "" || hoursErr != nil || minutesErr != nil || hours < 0 || minutes < 0 || minutes > 59 {
		http.Error(w, "invalid quota", http.StatusBadRequest)
		return
	}
	quota := hours*60*60 + minutes*60
	if err := a.repository.setUserQuota(user, quota); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, a.adminPath, http.StatusSeeOther)
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

func eventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var event focusEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if event.Type != "focus_changed" || event.DeviceID == "" || event.User == "" || event.PreviousApp == "" || event.ActiveSeconds < 0 || event.Timestamp.IsZero() {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}

	log.Printf("timestamp=%s device=%s user=%s app=%s active_seconds=%d", event.Timestamp.Format(time.RFC3339), event.DeviceID, event.User, event.PreviousApp, event.ActiveSeconds)
	w.WriteHeader(http.StatusOK)
}

func (a *application) heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var h heartbeat
	var dailyTotal int
	var policyVersion int
	var remainingSeconds int
	action := "allow"
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		log.Printf("invalid heartbeat: %v", err)
	} else if h.DeviceID == "" || h.User == "" || h.ActiveSeconds < 0 {
		log.Printf("invalid heartbeat: device_id and user must not be empty, and active_seconds must not be negative")
	} else {
		var err error
		h.ReportedAt = time.Now()
		dailyTotal, err = a.addHeartbeat(h)
		if err != nil {
			log.Printf("database error: %v", err)
		} else {
			quota, quotaErr := a.repository.userQuota(h.User)
			if quotaErr != nil {
				log.Printf("database error: %v", quotaErr)
			} else {
				action, remainingSeconds = screenTimeDecision(dailyTotal, quota)
			}
			policyVersion, err = a.repository.userPolicyVersion(h.User)
			if err != nil {
				log.Printf("database error: %v", err)
				policyVersion = 0
			}
			log.Printf("reported_at=%s device_id=%s user=%s active_seconds=%d daily_total_seconds=%d", h.ReportedAt.Format(time.RFC3339), h.DeviceID, h.User, h.ActiveSeconds, dailyTotal)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{Action: action, Message: "ok", DailyTotalSeconds: dailyTotal, PolicyVersion: policyVersion, RemainingSeconds: remainingSeconds})
}

func main() {
	databasePath := os.Getenv("DATABASE_PATH")
	if databasePath == "" {
		databasePath = "screengate.db"
	}
	repository, err := openRepository(databasePath)
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer repository.close()

	app := newApplication(repository)
	if adminPath := os.Getenv("ADMIN_PATH"); adminPath != "" && adminPath[0] == '/' {
		app.adminPath = adminPath
	}
	mux := http.NewServeMux()
	mux.HandleFunc(app.adminPath+"/downloads/install.ps1", downloadInstallerHandler)
	mux.HandleFunc(app.adminPath+"/downloads/screengate-client.exe", downloadClientHandler)
	mux.HandleFunc(app.adminPath+"/user-quota", app.userQuotaHandler)
	mux.HandleFunc("/event", eventHandler)
	mux.HandleFunc(app.adminPath, app.dashboardHandler)
	mux.HandleFunc("/heartbeat", app.heartbeatHandler)
	mux.HandleFunc("/", app.overviewHandler)

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
