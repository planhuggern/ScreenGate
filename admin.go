package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func parseMinute(value string, end bool) (int, error) {
	if value == "24:00" && end {
		return 1440, nil
	}
	t, err := time.Parse("15:04", value)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

func adminUserForm(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !requireMethod(w, r, http.MethodPost) {
		return "", false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return "", false
	}
	user := r.PostForm.Get("user")
	if !validIdentity(user) {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return "", false
	}
	return user, true
}

func (a *application) userPolicyHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := adminUserForm(w, r)
	if !ok {
		return
	}
	days := make([]weekdayPolicy, 0, 7)
	for day := 0; day < 7; day++ {
		prefix := fmt.Sprintf("day_%d_", day)
		policy := weekdayPolicy{Weekday: day, Configured: r.PostForm.Get(prefix+"enabled") == "on"}
		if policy.Configured {
			var err error
			policy.QuotaSeconds, err = parseQuota(r.PostForm.Get(prefix+"hours"), r.PostForm.Get(prefix+"minutes"))
			if err != nil {
				http.Error(w, "invalid daily quota", http.StatusBadRequest)
				return
			}
			policy.Disabled = r.PostForm.Get(prefix+"disabled") == "on"
			policy.StartMinute, err = parseMinute(r.PostForm.Get(prefix+"start"), false)
			if err != nil {
				http.Error(w, "invalid start time", http.StatusBadRequest)
				return
			}
			policy.EndMinute, err = parseMinute(r.PostForm.Get(prefix+"end"), true)
			if err != nil {
				http.Error(w, "invalid end time", http.StatusBadRequest)
				return
			}
		}
		days = append(days, policy)
	}
	if err := a.service.setWeekdayPolicies(user, days); err != nil {
		if errors.Is(err, errInvalidPolicy) {
			http.Error(w, "invalid policy: use different start/end times, or 00:00 to 24:00 for all day", http.StatusBadRequest)
		} else {
			http.Error(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	a.audit(user, "schedule", "weekly schedule updated")
	http.Redirect(w, r, a.adminPath, http.StatusSeeOther)
}

func (a *application) userBonusHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := adminUserForm(w, r)
	if !ok {
		return
	}
	var err error
	switch r.PostForm.Get("action") {
	case "reset":
		err = a.service.clearBonus(user)
	case "add", "":
		minutes, parseErr := strconv.Atoi(r.PostForm.Get("minutes"))
		if parseErr != nil || minutes < 1 || minutes > 1440 {
			http.Error(w, "invalid bonus minutes", http.StatusBadRequest)
			return
		}
		err = a.service.addBonusMinutes(user, minutes)
	default:
		http.Error(w, "invalid bonus action", http.StatusBadRequest)
		return
	}
	if err != nil {
		if errors.Is(err, errInvalidPolicy) {
			http.Error(w, "daily bonus limit reached", http.StatusBadRequest)
		} else {
			http.Error(w, "database error", http.StatusInternalServerError)
		}
		return
	}
	a.audit(user, "bonus", r.PostForm.Get("action")+" "+r.PostForm.Get("minutes"))
	http.Redirect(w, r, a.adminPath, http.StatusSeeOther)
}

func (a *application) userPauseHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := adminUserForm(w, r)
	if !ok {
		return
	}
	value := r.PostForm.Get("paused")
	if value != "true" && value != "false" {
		http.Error(w, "invalid pause value", http.StatusBadRequest)
		return
	}
	if err := a.service.setUserPaused(user, value == "true"); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	a.audit(user, "pause", value)
	http.Redirect(w, r, a.adminPath, http.StatusSeeOther)
}

func (a *application) userDeleteHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := adminUserForm(w, r)
	if !ok {
		return
	}
	if r.PostForm.Get("confirm") != "delete" {
		http.Error(w, "confirmation required", http.StatusBadRequest)
		return
	}
	if err := a.service.repository.deleteUser(user); err != nil {
		log.Printf("delete user %q: %v", user, err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, a.adminPath, http.StatusSeeOther)
}

func (a *application) pairDeviceHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := adminUserForm(w, r)
	if !ok {
		return
	}
	var known int
	if err := a.service.repository.db.QueryRow("SELECT EXISTS(SELECT 1 FROM user_quotas WHERE user = ? UNION SELECT 1 FROM user_presence WHERE user = ?)", user, user).Scan(&known); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	if known == 0 {
		if err := a.service.setUserQuota(user, 3600); err != nil {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
	}
	code, err := a.service.repository.createPairingCode(user, a.service.now())
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	a.audit(user, "pairing", "pairing code created")
	a.renderDashboard(w, r, "Koblingskoden er klar. Den kan brukes én gang innen 15 minutter.", code, user)
}

func (a *application) revokeDeviceHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := r.PostForm.Get("id")
	if len(id) != 32 {
		http.Error(w, "invalid device", http.StatusBadRequest)
		return
	}
	result, err := a.service.repository.db.Exec("UPDATE devices SET revoked = 1 WHERE id = ?", id)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		http.NotFound(w, r)
		return
	}
	a.audit("", "device_revoked", id)
	http.Redirect(w, r, a.adminPath, http.StatusSeeOther)
}

func (a *application) audit(user, action, detail string) {
	if _, err := a.service.repository.db.Exec("INSERT INTO audit_events(occurred_at,user,action,detail) VALUES(?,?,?,?)", a.service.now().Unix(), user, action, detail); err != nil {
		log.Printf("audit write failed: %v", err)
	}
}

// Spreadsheet programs may evaluate a cell beginning with a formula marker.
func csvText(value string) string {
	if value != "" && strings.ContainsAny(value[:1], "=+-@\t\r\n") {
		return "'" + value
	}
	return value
}

func (a *application) exportHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	activities, err := a.service.todaysActivities()
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	var content bytes.Buffer
	writer := csv.NewWriter(&content)
	_ = writer.Write([]string{"date", "user", "screen_time_seconds", "timezone"})
	for _, item := range activities {
		for _, day := range item.History {
			_ = writer.Write([]string{day.Date, csvText(item.User), strconv.Itoa(day.TotalSeconds), a.service.location.String()})
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		http.Error(w, "export error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="screengate-week.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(content.Bytes())
}
