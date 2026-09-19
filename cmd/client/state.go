package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const maxLeaseSeconds = 120

// The absolute expiry survives restarts. Keeping a separate pending report makes
// a retry idempotent even when the server applied it but its response was lost.
type clientState struct {
	Identity string `json:"identity"`
	Action   string `json:"action"`
	Reason   string `json:"reason"`
	// ServerContacted distinguishes a fresh/unprovisioned client from a
	// client that has received an explicit server decision. A fresh client
	// must not lock the user out while its first heartbeat is in flight.
	ServerContacted  bool       `json:"server_contacted"`
	PolicyVersion    int        `json:"policy_version"`
	PolicyDate       string     `json:"policy_date"`
	QuotaSeconds     int        `json:"quota_seconds"`
	RemainingSeconds int        `json:"remaining_seconds"`
	LeaseExpiresAt   time.Time  `json:"lease_expires_at"`
	ScheduledLockAt  time.Time  `json:"scheduled_lock_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	PendingSeconds   int        `json:"pending_seconds"`
	PendingDate      string     `json:"pending_date,omitempty"`
	Report           *heartbeat `json:"report,omitempty"`
}

type stateEnvelope struct {
	State json.RawMessage `json:"state"`
	MAC   string          `json:"mac"`
}

func stateIdentity(endpoint, deviceID, username string) string {
	digest := sha256.Sum256([]byte(endpoint + "\x00" + deviceID + "\x00" + username))
	return hex.EncodeToString(digest[:])
}

func (s *clientState) allowed(now time.Time) bool {
	// Fail open until the server has made at least one valid decision. This
	// covers startup, pairing races, and a temporarily unreachable server.
	if !s.ServerContacted {
		return true
	}
	if now.Before(s.UpdatedAt) {
		s.invalidate("clock_changed")
	}
	return s.Action == "allow" && now.Before(s.LeaseExpiresAt) && (s.QuotaSeconds == 0 || s.RemainingSeconds > 0)
}

func (s *clientState) invalidate(reason string) {
	s.Action = "lock"
	s.Reason = reason
	s.LeaseExpiresAt = time.Time{}
}

func (s *clientState) markServerUnavailable() {
	s.ServerContacted = false
	s.Action = "allow"
	s.Reason = "server_unavailable"
	s.LeaseExpiresAt = time.Time{}
	s.ScheduledLockAt = time.Time{}
}

func (s clientState) warningRemaining(now time.Time) int {
	remaining := s.RemainingSeconds
	if s.QuotaSeconds == 0 {
		remaining = 86400
	}
	if !s.ScheduledLockAt.IsZero() {
		remaining = min(remaining, max(0, int(s.ScheduledLockAt.Sub(now)/time.Second)))
	}
	return remaining
}

func (s *clientState) account(seconds int, now time.Time) {
	if now.Before(s.UpdatedAt) {
		s.invalidate("clock_changed")
	}
	if seconds > 0 {
		if s.PendingSeconds == 0 {
			s.PendingDate = s.PolicyDate
		}
		s.PendingSeconds = min(86400, s.PendingSeconds+seconds)
		if s.QuotaSeconds > 0 {
			s.RemainingSeconds = max(0, s.RemainingSeconds-seconds)
		}
	}
	s.UpdatedAt = now
}

func (s *clientState) prepareReport(deviceID, username, sessionState string, now time.Time) (heartbeat, error) {
	if s.Report == nil {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return heartbeat{}, err
		}
		s.Report = &heartbeat{
			DeviceID: deviceID, User: username, HeartbeatID: hex.EncodeToString(random[:]),
			SessionState: sessionState, ActivityDate: s.PendingDate, ActiveSeconds: s.PendingSeconds, ReportedAt: now,
		}
		s.PendingSeconds = 0
		s.PendingDate = ""
	}
	// Session state is informational, not part of the idempotent usage charge.
	s.Report.SessionState = sessionState
	return *s.Report, nil
}

func (s *clientState) apply(result response, sentAt, receivedAt time.Time) {
	s.Report = nil
	s.ServerContacted = true
	s.Action = result.Action
	s.Reason = result.Reason
	s.PolicyVersion = result.PolicyVersion
	s.PolicyDate = result.PolicyDate
	s.QuotaSeconds = result.QuotaSeconds
	s.RemainingSeconds = result.RemainingSeconds
	if s.PendingDate == "" || s.PendingDate == result.PolicyDate {
		s.RemainingSeconds = max(0, s.RemainingSeconds-s.PendingSeconds)
	}
	s.UpdatedAt = receivedAt
	s.ScheduledLockAt = time.Time{}
	if !result.NextLockAt.IsZero() && !result.ServerTime.IsZero() {
		s.ScheduledLockAt = sentAt.Add(result.NextLockAt.Sub(result.ServerTime))
	}
	lease := min(result.LeaseSeconds, maxLeaseSeconds)
	if result.QuotaSeconds > 0 {
		lease = min(lease, s.RemainingSeconds)
	}
	// Network delay never extends a lease, and a slow response can already be expired.
	s.LeaseExpiresAt = sentAt.Add(time.Duration(lease) * time.Second)
	if !result.NextTransitionAt.IsZero() && !result.ServerTime.IsZero() {
		transition := sentAt.Add(result.NextTransitionAt.Sub(result.ServerTime))
		if transition.Before(s.LeaseExpiresAt) {
			s.LeaseExpiresAt = transition
		}
	}
	if result.Action != "allow" {
		s.LeaseExpiresAt = time.Time{}
	}
}

func stateMAC(data []byte, token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func loadState(path, identity, token string, now time.Time) (clientState, error) {
	initial := clientState{Identity: identity, Action: "lock", Reason: "awaiting_server", UpdatedAt: now}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return initial, nil
	}
	if err != nil {
		// A state file that exists but cannot be authenticated or read is not
		// equivalent to a fresh install. Keep this fail-closed.
		initial.ServerContacted = true
		return initial, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 64*1024 {
		initial.ServerContacted = true
		return initial, errors.New("invalid state file size")
	}
	var envelope stateEnvelope
	if err := json.NewDecoder(file).Decode(&envelope); err != nil {
		initial.ServerContacted = true
		return initial, fmt.Errorf("invalid state: %w", err)
	}
	if !hmac.Equal([]byte(envelope.MAC), []byte(stateMAC(envelope.State, token))) {
		initial.ServerContacted = true
		return initial, errors.New("saved state authentication failed")
	}
	var state clientState
	if err := json.Unmarshal(envelope.State, &state); err != nil {
		initial.ServerContacted = true
		return initial, err
	}
	if state.Identity != identity || state.PendingSeconds < 0 || state.PendingSeconds > 86400 || state.RemainingSeconds < 0 || state.QuotaSeconds < 0 || (state.Action != "allow" && state.Action != "lock") {
		initial.ServerContacted = true
		return initial, errors.New("saved state does not match this client")
	}
	if state.Report != nil && (state.Report.HeartbeatID == "" || state.Report.ActiveSeconds < 0 || state.Report.ActiveSeconds > 86400) {
		initial.ServerContacted = true
		return initial, errors.New("invalid pending report")
	}
	// Migrate state files written before ServerContacted existed. The old
	// initial state was lock/awaiting_server with no lease; all other valid
	// states represent a prior server decision.
	if !state.ServerContacted {
		state.ServerContacted = !(state.Action == "lock" && state.Reason == "awaiting_server" && state.LeaseExpiresAt.IsZero())
	}
	state.allowed(now)
	return state, nil
}

func saveState(path, token string, state clientState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(stateEnvelope{State: data, MAC: stateMAC(data, token)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(envelope)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
