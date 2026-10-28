package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Offline intervals need more room than the former single pending counter.
// Reports remain limited to 32 intervals; this is only the durable local queue.
const maxStateBytes = 1 << 20

// This envelope is the existing on-disk format. The MAC covers the exact JSON
// bytes in State; loading must verify them before interpreting their fields.
type stateEnvelope struct {
	State json.RawMessage `json:"state"`
	MAC   string          `json:"mac"`
}

func loadState(path, identity, token string, now time.Time) (clientState, error) {
	initial := clientState{Identity: identity, Action: "lock", Reason: "awaiting_server", UpdatedAt: now}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return initial, nil
	}
	// An unreadable or unauthenticated file must retain the existing closed
	// fallback, rather than the fresh-install behavior for a missing file.
	initial.ServerContacted = true
	if err != nil {
		return initial, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > maxStateBytes {
		return initial, errors.New("invalid state file size")
	}

	state, err := readSignedState(file, token)
	if err != nil {
		return initial, err
	}
	if err := validateSavedState(state, identity); err != nil {
		return initial, err
	}
	state.restoreAfterLoad(now)
	return state, nil
}

func readSignedState(reader io.Reader, token string) (clientState, error) {
	var envelope stateEnvelope
	if err := json.NewDecoder(reader).Decode(&envelope); err != nil {
		return clientState{}, fmt.Errorf("invalid state: %w", err)
	}
	if !hmac.Equal([]byte(envelope.MAC), []byte(stateMAC(envelope.State, token))) {
		return clientState{}, errors.New("saved state authentication failed")
	}
	var state clientState
	if err := json.Unmarshal(envelope.State, &state); err != nil {
		return clientState{}, err
	}
	return state, nil
}

func validateSavedState(state clientState, identity string) error {
	identityMatches := state.Identity == identity
	pendingWithinLimit := state.PendingSeconds >= 0 && state.PendingSeconds <= 86400
	quotaNonnegative := state.RemainingSeconds >= 0 && state.QuotaSeconds >= 0
	actionKnown := state.Action == "allow" || state.Action == "lock"
	if !identityMatches || !pendingWithinLimit || !quotaNonnegative || !actionKnown {
		return errors.New("saved state does not match this client")
	}
	if state.Report != nil && (state.Report.HeartbeatID == "" || state.Report.ActiveSeconds < 0 || state.Report.ActiveSeconds > 86400) {
		return errors.New("invalid pending report")
	}
	if !state.UsageStarted && (state.PendingUsage != nil || len(state.UnreportedUsage) != 0) {
		return errors.New("invalid saved usage state")
	}
	if state.PendingUsage != nil && (state.PendingUsage.ID == "" || len(state.PendingUsage.Intervals) > 32) {
		return errors.New("invalid pending usage batch")
	}
	if state.Report != nil && state.Report.Usage != nil && (state.PendingUsage == nil || state.Report.Usage.ID != state.PendingUsage.ID) {
		return errors.New("pending heartbeat does not match saved usage")
	}
	intervals := state.UnreportedUsage
	if state.PendingUsage != nil {
		intervals = append(append([]usageInterval(nil), state.PendingUsage.Intervals...), intervals...)
	}
	var previous time.Time
	for _, interval := range intervals {
		if !interval.End.After(interval.Start) || interval.End.Sub(interval.Start) > time.Hour || (!previous.IsZero() && interval.Start.Before(previous)) {
			return errors.New("invalid saved usage interval")
		}
		previous = interval.End
	}
	return nil
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
	if len(envelope) > maxStateBytes {
		return errors.New("saved state exceeds size limit")
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

func stateMAC(data []byte, token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func stateIdentity(endpoint, deviceID, username string) string {
	digest := sha256.Sum256([]byte(endpoint + "\x00" + deviceID + "\x00" + username))
	return hex.EncodeToString(digest[:])
}
