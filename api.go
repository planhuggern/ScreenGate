package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"
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
	if !validIdentity(h.DeviceID) || !validIdentity(h.User) {
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
