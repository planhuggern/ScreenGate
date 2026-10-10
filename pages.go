package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
)

func (a *application) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != a.adminPath {
		http.NotFound(w, r)
		return
	}
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	a.renderDashboard(w, r, "")
}

func (a *application) renderDashboard(w http.ResponseWriter, r *http.Request, flash string) {
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
	model := dashboard{Date: a.service.today(), Activities: activities, AdminPath: a.adminPath, CSRFToken: a.csrfToken, Timezone: a.service.location.String(), Flash: flash, Devices: devices}
	model.ClientSHA256, _ = clientChecksum()
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

func faviconHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.WriteString(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40"><rect width="40" height="40" rx="12" fill="#306b4e"/><g fill="none" stroke="#fff" stroke-width="2"><circle cx="20" cy="20" r="12"/><path d="M20 11v9l6 4"/></g></svg>`)
}
