package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type heartbeat struct {
	DeviceID      string    `json:"device_id"`
	User          string    `json:"user"`
	HeartbeatID   string    `json:"heartbeat_id"`
	ActivityDate  string    `json:"activity_date,omitempty"`
	SessionState  string    `json:"session_state"`
	ActiveSeconds int       `json:"active_seconds"`
	ReportedAt    time.Time `json:"reported_at"`
}

type response struct {
	Action           string    `json:"action"`
	PolicyVersion    int       `json:"policy_version"`
	RemainingSeconds int       `json:"remaining_seconds"`
	QuotaSeconds     int       `json:"quota_seconds"`
	LeaseSeconds     int       `json:"lease_seconds"`
	Reason           string    `json:"reason"`
	ServerTime       time.Time `json:"server_time"`
	PolicyDate       string    `json:"policy_date"`
	NextTransitionAt time.Time `json:"next_transition_at"`
	NextLockAt       time.Time `json:"next_lock_at"`
}

type focusEvent struct {
	Type          string    `json:"type"`
	DeviceID      string    `json:"device_id"`
	User          string    `json:"user"`
	PreviousApp   string    `json:"previous_app"`
	ActiveSeconds int       `json:"active_seconds"`
	Timestamp     time.Time `json:"timestamp"`
}

type httpStatusError struct{ status int }

func (e httpStatusError) Error() string { return fmt.Sprintf("server returned HTTP %d", e.status) }

func authenticationFailed(err error) bool {
	var status httpStatusError
	return errors.As(err, &status) && (status.status == http.StatusUnauthorized || status.status == http.StatusForbidden)
}

func validateEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("server must be an http(s) URL without credentials, query or fragment")
	}
	if parsed.Path != "/heartbeat" {
		return "", errors.New("server URL must end in /heartbeat")
	}
	return parsed.String(), nil
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 8 * time.Second,
		// A login page or redirected origin is never an authorization decision.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func postJSON(ctx context.Context, client *http.Client, endpoint, token string, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("User-Agent", "ScreenGate-Client/2")
	result, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if result.StatusCode != http.StatusOK {
		result.Body.Close()
		return nil, httpStatusError{status: result.StatusCode}
	}
	return result, nil
}

func postHeartbeat(ctx context.Context, client *http.Client, endpoint, token string, report heartbeat) (response, error) {
	httpResponse, err := postJSON(ctx, client, endpoint, token, report)
	if err != nil {
		return response{}, err
	}
	defer httpResponse.Body.Close()
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(httpResponse.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		return response{}, errors.New("heartbeat response is not JSON")
	}
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, 16*1024+1))
	if err != nil {
		return response{}, err
	}
	if len(body) > 16*1024 {
		return response{}, errors.New("heartbeat response exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var result response
	if err := decoder.Decode(&result); err != nil {
		return response{}, fmt.Errorf("invalid heartbeat response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return response{}, errors.New("heartbeat response contains trailing data")
	}
	if result.Action != "allow" && result.Action != "lock" {
		return response{}, errors.New("unknown server action")
	}
	if result.RemainingSeconds < 0 || result.QuotaSeconds < 0 || result.PolicyVersion < 0 || result.LeaseSeconds < 0 {
		return response{}, errors.New("server returned a negative authorization value")
	}
	if result.Action == "allow" && (result.LeaseSeconds == 0 || (result.QuotaSeconds > 0 && result.RemainingSeconds == 0)) {
		return response{}, errors.New("allow response has no usable authorization lease")
	}
	return result, nil
}

func postEvent(ctx context.Context, client *http.Client, endpoint, token string, event focusEvent) error {
	result, err := postJSON(ctx, client, endpoint, token, event)
	if err != nil {
		return err
	}
	defer result.Body.Close()
	_, err = io.Copy(io.Discard, io.LimitReader(result.Body, 4096))
	return err
}
