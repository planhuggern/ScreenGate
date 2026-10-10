package main

import (
	"log"
	"net/http"
)

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
	mux.HandleFunc("/downloads/update.ps1", func(w http.ResponseWriter, r *http.Request) { downloadScript(w, r, "update.ps1") })
	mux.HandleFunc("/downloads/update-key.json", a.downloadUpdateKey)
	mux.HandleFunc("/downloads/update.json", a.downloadUpdateManifest)
	mux.HandleFunc("/downloads/uninstall.ps1", downloadUninstallerHandler)
	mux.HandleFunc("/downloads/screengate-client.exe", downloadClientHandler)
	mux.HandleFunc("/downloads/screengate-client.exe.sha256", downloadChecksumHandler)
	mux.HandleFunc(a.adminPath+"/downloads/install.ps1", a.requireAdmin(downloadInstallerHandler))
	mux.HandleFunc(a.adminPath+"/downloads/screengate-client.exe", a.requireAdmin(downloadClientHandler))
	mux.HandleFunc(a.adminPath+"/user-quota", a.requireAdmin(a.userQuotaHandler))
	mux.HandleFunc(a.adminPath+"/user-policy", a.requireAdmin(a.userPolicyHandler))
	mux.HandleFunc(a.adminPath+"/user-bonus", a.requireAdmin(a.userBonusHandler))
	mux.HandleFunc(a.adminPath+"/user-pause", a.requireAdmin(a.userPauseHandler))
	mux.HandleFunc(a.adminPath+"/user-delete", a.requireAdmin(a.userDeleteHandler))
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
	return securityHeaders(protection.Handler(mux))
}
