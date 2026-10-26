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
	if err != nil || info.Size() > 64*1024 {
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
