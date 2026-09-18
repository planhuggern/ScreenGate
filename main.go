package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"
)

type response struct {
	Action            string    `json:"action"`
	Message           string    `json:"message"`
	DailyTotalSeconds int       `json:"daily_total_seconds"`
	PolicyVersion     int       `json:"policy_version"`
	RemainingSeconds  int       `json:"remaining_seconds"`
	QuotaSeconds      int       `json:"quota_seconds"`
	Unlimited         bool      `json:"unlimited"`
	LeaseSeconds      int       `json:"lease_seconds"`
	Reason            string    `json:"reason"`
	ServerTime        time.Time `json:"server_time"`
	PolicyDate        string    `json:"policy_date"`
	NextAllowedAt     time.Time `json:"next_allowed_at,omitempty"`
	NextTransitionAt  time.Time `json:"next_transition_at,omitempty"`
	NextLockAt        time.Time `json:"next_lock_at,omitempty"`
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
	service           *screenTimeService
	adminPath         string
	adminUser         string
	adminPassword     string
	csrfToken         string
	loginLimiter      requestLimiter
	enrollmentLimiter requestLimiter
}

func newApplication(repository *repository) *application {
	return &application{service: newScreenTimeService(repository), adminPath: "/admin", adminUser: "admin", csrfToken: randomHex(32), loginLimiter: requestLimiter{limit: 10}, enrollmentLimiter: requestLimiter{limit: 10}}
}

func (a *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.healthHandler)
	mux.HandleFunc("/favicon.svg", faviconHandler)
	mux.HandleFunc("/favicon.ico", faviconHandler)
	mux.HandleFunc("/enroll", a.enrollHandler)
	mux.HandleFunc("/heartbeat", a.requireDevice(a.heartbeatHandler))
	mux.HandleFunc("/event", a.requireDevice(eventHandler))
	mux.HandleFunc("/downloads/install.ps1", downloadInstallerHandler)
	mux.HandleFunc("/downloads/uninstall.ps1", downloadUninstallerHandler)
	mux.HandleFunc("/downloads/screengate-client.exe", downloadClientHandler)
	mux.HandleFunc("/downloads/screengate-client.exe.sha256", downloadChecksumHandler)
	mux.HandleFunc(a.adminPath+"/downloads/install.ps1", a.requireAdmin(downloadInstallerHandler))
	mux.HandleFunc(a.adminPath+"/downloads/screengate-client.exe", a.requireAdmin(downloadClientHandler))
	mux.HandleFunc(a.adminPath+"/user-quota", a.requireAdmin(a.userQuotaHandler))
	mux.HandleFunc(a.adminPath+"/user-policy", a.requireAdmin(a.userPolicyHandler))
	mux.HandleFunc(a.adminPath+"/user-bonus", a.requireAdmin(a.userBonusHandler))
	mux.HandleFunc(a.adminPath+"/user-pause", a.requireAdmin(a.userPauseHandler))
	mux.HandleFunc(a.adminPath+"/devices/pair", a.requireAdmin(a.pairDeviceHandler))
	mux.HandleFunc(a.adminPath+"/devices/revoke", a.requireAdmin(a.revokeDeviceHandler))
	mux.HandleFunc(a.adminPath+"/export.csv", a.requireAdmin(a.exportHandler))
	mux.HandleFunc(a.adminPath+"/backup", a.requireAdmin(a.backupHandler))
	mux.HandleFunc(a.adminPath, a.requireAdmin(a.dashboardHandler))
	mux.HandleFunc("/", a.overviewHandler)
	protection := http.NewCrossOriginProtection()
	for _, origin := range configuredTrustedOrigins() {
		if err := protection.AddTrustedOrigin(origin); err != nil {
			log.Printf("cross-origin protection: ignoring invalid trusted origin %q: %v", origin, err)
		}
	}
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("cross-origin request denied: method=%s host=%q origin=%q sec-fetch-site=%q", r.Method, r.Host, r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site"))
		http.Error(w, "cross-origin request detected, and/or browser is out of date: Sec-Fetch-Site is missing, and Origin does not match Host", http.StatusForbidden)
	}))
	return securityHeaders(protection.Handler(mux))
}

func (a *application) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != a.adminPath {
		http.NotFound(w, r)
		return
	}
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	a.renderDashboard(w, r, "", "", "")
}

func (a *application) renderDashboard(w http.ResponseWriter, r *http.Request, flash, code, user string) {
	activities, err := a.service.todaysActivities()
	if err != nil {
		log.Printf("dashboard: %v", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	devices, err := a.service.repository.deviceViews()
	if err != nil {
		log.Printf("devices: %v", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	model := dashboard{Date: a.service.today(), Activities: activities, AdminPath: a.adminPath, CSRFToken: a.csrfToken, Timezone: a.service.location.String(), Flash: flash, PairingCode: code, PairingUser: user, Devices: devices}
	model.Audit, err = a.recentAudit()
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	var page bytes.Buffer
	if err := dashboardTemplate.Execute(&page, model); err != nil {
		log.Printf("dashboard template: %v", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(page.Bytes())
}

func (a *application) overviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := overviewTemplate.Execute(w, dashboard{AdminPath: a.adminPath}); err != nil {
		log.Printf("overview: %v", err)
	}
}

func (a *application) userQuotaHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	user := r.PostForm.Get("user")
	quota, err := parseQuota(r.PostForm.Get("hours"), r.PostForm.Get("minutes"))
	if !validIdentity(user) || err != nil {
		http.Error(w, "invalid quota", http.StatusBadRequest)
		return
	}
	if err := a.service.setUserQuota(user, quota); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	a.audit(user, "quota", strconv.Itoa(quota))
	http.Redirect(w, r, a.adminPath, http.StatusSeeOther)
}

func parseQuota(hoursText, minutesText string) (int, error) {
	h, hErr := strconv.Atoi(hoursText)
	m, mErr := strconv.Atoi(minutesText)
	if hErr != nil || mErr != nil || h < 0 || h > 24 || m < 0 || m > 59 || (h == 24 && m != 0) {
		return 0, errors.New("quota must be between 0 and 24 hours")
	}
	return h*3600 + m*60, nil
}

//go:embed cmd/client/install.ps1 cmd/client/uninstall.ps1
var installerFiles embed.FS

func clientBinaryPath() string {
	if path := os.Getenv("CLIENT_BINARY_PATH"); path != "" {
		return path
	}
	if _, err := os.Stat("/client/screengate-client.exe"); err == nil {
		return "/client/screengate-client.exe"
	}
	return "screengate-client.exe"
}

func downloadClientHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Content-Type", "application/vnd.microsoft.portable-executable")
	w.Header().Set("Content-Disposition", "attachment; filename=screengate-client.exe")
	http.ServeFile(w, r, clientBinaryPath())
}

func downloadChecksumHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	f, err := os.Open(clientBinaryPath())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		http.Error(w, "checksum unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "%s  screengate-client.exe\n", hex.EncodeToString(h.Sum(nil)))
}

func downloadInstallerHandler(w http.ResponseWriter, r *http.Request) {
	downloadScript(w, r, "install.ps1")
}

func downloadUninstallerHandler(w http.ResponseWriter, r *http.Request) {
	downloadScript(w, r, "uninstall.ps1")
}

func downloadScript(w http.ResponseWriter, r *http.Request, name string) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	data, err := installerFiles.ReadFile("cmd/client/" + name)
	if err != nil {
		http.Error(w, "installer unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	_, _ = w.Write(data)
}

func faviconHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.WriteString(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40"><rect width="40" height="40" rx="12" fill="#306b4e"/><g fill="none" stroke="#fff" stroke-width="2"><circle cx="20" cy="20" r="12"/><path d="M20 11v9l6 4"/></g></svg>`)
}

func eventHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var event focusEvent
	if err := decodeJSON(w, r, &event); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if event.Type != "focus_changed" || !validIdentity(event.DeviceID) || !validIdentity(event.User) || !validIdentity(event.PreviousApp) || event.ActiveSeconds < 0 || event.ActiveSeconds > 86400 || event.Timestamp.IsZero() {
		writeAPIError(w, http.StatusBadRequest, "invalid event")
		return
	}
	if !bindDeviceIdentity(r, &event.User, &event.DeviceID) {
		writeAPIError(w, http.StatusForbidden, "device identity mismatch")
		return
	}
	// Quotas need time, not a history of applications or browsing.
	w.WriteHeader(http.StatusOK)
}

func (a *application) heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var h heartbeat
	if err := decodeJSON(w, r, &h); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if !validIdentity(h.DeviceID) || !validIdentity(h.User) || h.ActiveSeconds < 0 || h.ActiveSeconds > 86400 {
		writeAPIError(w, http.StatusBadRequest, "invalid heartbeat")
		return
	}
	if !bindDeviceIdentity(r, &h.User, &h.DeviceID) {
		writeAPIError(w, http.StatusForbidden, "device identity mismatch")
		return
	}
	result, err := a.service.recordHeartbeat(h)
	if err != nil {
		if errors.Is(err, errInvalidHeartbeat) {
			writeAPIError(w, http.StatusBadRequest, "invalid heartbeat")
			return
		}
		log.Printf("heartbeat: %v", err)
		writeAPIError(w, http.StatusServiceUnavailable, "screen-time decision unavailable")
		return
	}
	a.recordDeviceSeen(r, result.ReportedAt)
	writeJSON(w, http.StatusOK, response{Action: result.Action, Message: "ok", DailyTotalSeconds: result.DailyTotalSeconds, PolicyVersion: result.PolicyVersion, RemainingSeconds: result.RemainingSeconds, QuotaSeconds: result.QuotaSeconds, Unlimited: result.Unlimited, LeaseSeconds: result.LeaseSeconds, Reason: result.Reason, ServerTime: result.ServerTime, PolicyDate: result.PolicyDate, NextAllowedAt: result.NextAllowedAt, NextTransitionAt: result.NextTransitionAt, NextLockAt: result.NextLockAt})
}

func (a *application) healthHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.service.repository.db.PingContext(ctx); err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func run() error {
	configuration, err := loadConfiguration()
	if err != nil {
		return err
	}
	time.Local = configuration.location
	if err := os.MkdirAll(filepath.Dir(configuration.databasePath), 0700); err != nil {
		return err
	}
	repository, err := openRepository(configuration.databasePath)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer repository.close()
	app := newApplication(repository)
	app.adminPath, app.adminUser, app.adminPassword = configuration.adminPath, configuration.adminUser, configuration.adminPassword
	server := &http.Server{Addr: configuration.listenAddr, Handler: app.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("ScreenGate listening on %s (timezone %s)", server.Addr, time.Local)
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErrors:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func main() {
	if err := run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

type configuration struct {
	databasePath, adminPath, adminUser, adminPassword, listenAddr string
	location                                                      *time.Location
}

func configuredTrustedOrigins() []string {
	value := os.Getenv("SCREENGATE_TRUSTED_ORIGINS")
	if value == "" {
		return nil
	}
	origins := make([]string, 0, 4)
	for _, item := range strings.Split(value, ",") {
		if origin := strings.TrimSpace(item); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

func loadConfiguration() (configuration, error) {
	get := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	c := configuration{databasePath: get("DATABASE_PATH", "screengate.db"), adminPath: get("ADMIN_PATH", "/admin"), adminUser: get("ADMIN_USER", "admin"), adminPassword: os.Getenv("ADMIN_PASSWORD"), listenAddr: get("LISTEN_ADDR", ":8080")}
	if len(c.adminPassword) < 12 {
		return c, errors.New("set ADMIN_PASSWORD to at least 12 characters before starting ScreenGate")
	}
	if !validIdentity(c.adminUser) {
		return c, errors.New("invalid ADMIN_USER")
	}
	if !validAdminPath(c.adminPath) {
		return c, errors.New("ADMIN_PATH must be a simple path such as /admin and must not conflict with API paths")
	}
	var err error
	c.location, err = time.LoadLocation(get("SCREENGATE_TIMEZONE", "Europe/Oslo"))
	if err != nil {
		return c, fmt.Errorf("invalid SCREENGATE_TIMEZONE: %w", err)
	}
	return c, nil
}

func validAdminPath(path string) bool {
	if len(path) < 2 || len(path) > 128 || path[0] != '/' || strings.Contains(path[1:], "/") {
		return false
	}
	for _, c := range path[1:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	switch path {
	case "/heartbeat", "/event", "/enroll", "/downloads", "/healthz":
		return false
	}
	return true
}
