package main

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const maxLeaseSeconds = 120

// The last server decision and locally consumed quota survive restarts. A pending
// report keeps its ID when a response is lost and the report is retried.
type clientState struct {
	Identity string `json:"identity"`
	Action   string `json:"action"`
	Reason   string `json:"reason"`
	// Connection failure and the presence of a prior decision are independent.
	ServerUnavailable bool       `json:"server_unavailable,omitempty"`
	ServerContacted   bool       `json:"server_contacted"`
	PolicyVersion     int        `json:"policy_version"`
	PolicyDate        string     `json:"policy_date"`
	QuotaSeconds      int        `json:"quota_seconds"`
	RemainingSeconds  int        `json:"remaining_seconds"`
	LeaseExpiresAt    time.Time  `json:"lease_expires_at"`
	ScheduledLockAt   time.Time  `json:"scheduled_lock_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	PendingSeconds    int        `json:"pending_seconds"`
	PendingDate       string     `json:"pending_date,omitempty"`
	Report            *heartbeat `json:"report,omitempty"`
}

func (s *clientState) allowed(now time.Time) bool {
	if !s.ServerContacted {
		return false
	}
	if now.Before(s.UpdatedAt) {
		s.invalidate("clock_changed")
	}
	if s.Action != "allow" {
		return false
	}
	if s.QuotaSeconds > 0 && s.RemainingSeconds <= 0 {
		return false
	}
	if !s.ScheduledLockAt.IsZero() && !now.Before(s.ScheduledLockAt) {
		return false
	}
	return true
}

func (s *clientState) invalidate(reason string) {
	s.Action = "lock"
	s.Reason = reason
	s.LeaseExpiresAt = time.Time{}
}

func (s *clientState) markServerUnavailable() {
	// A failed request provides no new decision, quota or schedule.
	s.ServerUnavailable = true
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
	if s.Report == nil || s.Report.SessionState != sessionState {
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
	// A changed session state must have a fresh ID so the server observes it.
	s.Report.SessionState = sessionState
	return *s.Report, nil
}

func (s *clientState) apply(result response, sentAt, receivedAt time.Time) {
	s.Report = nil
	s.ServerContacted = true
	s.ServerUnavailable = false
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
	// Retain this field in saved files for compatibility. Short leases no longer
	// decide access; local rest quota and ScheduledLockAt do.
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

// Restore compatibility fields and check the clock without refilling quota.
// Old fail-open files have lost their decision and require a fresh server reply.
func (s *clientState) restoreAfterLoad(now time.Time) {
	// Older clients erased the previous decision when contact failed. That
	// permission cannot safely be reconstructed from their saved rest quota.
	if s.Reason == "server_unavailable" {
		s.invalidate("legacy_offline_state")
		s.ServerContacted = true
	}
	// Migrate state files written before ServerContacted existed. The old
	// initial state was lock/awaiting_server with no lease; all other valid
	// states represent a prior server decision.
	if !s.ServerContacted {
		s.ServerContacted = !(s.Action == "lock" && s.Reason == "awaiting_server" && s.LeaseExpiresAt.IsZero())
	}
	s.allowed(now)
}
