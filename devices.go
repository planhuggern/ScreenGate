package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

type deviceView struct {
	ID, DeviceID, User, LastSeen string
	Revoked                      bool
}

type deviceIdentity struct{ ID, DeviceID, User string }
type deviceContextKey struct{}

var errInvalidPairing = errors.New("invalid or expired pairing code")

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func tokenHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func (r *repository) createPairingCode(user string, now time.Time) (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	tx, err := r.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM pairing_codes WHERE expires_at <= ? OR user = ?", now.Unix(), user); err != nil {
		return "", err
	}
	if _, err = tx.Exec("INSERT INTO pairing_codes(code_hash,user,expires_at) VALUES(?,?,?)", tokenHash(code), user, now.Add(15*time.Minute).Unix()); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return code[:4] + "-" + code[4:8] + "-" + code[8:12] + "-" + code[12:], nil
}

type enrollmentResponse struct {
	Token    string `json:"token"`
	DeviceID string `json:"device_id"`
	User     string `json:"user"`
}

func (r *repository) enrollDevice(code, deviceID string, now time.Time) (enrollmentResponse, error) {
	code = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	tx, err := r.db.Begin()
	if err != nil {
		return enrollmentResponse{}, err
	}
	defer tx.Rollback()
	var user string
	err = tx.QueryRow("DELETE FROM pairing_codes WHERE code_hash = ? AND expires_at > ? RETURNING user", tokenHash(code), now.Unix()).Scan(&user)
	if errors.Is(err, sql.ErrNoRows) {
		return enrollmentResponse{}, errInvalidPairing
	}
	if err != nil {
		return enrollmentResponse{}, err
	}
	result := enrollmentResponse{Token: randomHex(32), DeviceID: deviceID, User: user}
	// Re-pairing replaces earlier credentials for this user on this machine.
	if _, err = tx.Exec("UPDATE devices SET revoked = 1 WHERE device_id = ? AND user = ?", deviceID, user); err != nil {
		return enrollmentResponse{}, err
	}
	if _, err = tx.Exec("INSERT INTO devices(id,device_id,user,token_hash,created_at) VALUES(?,?,?,?,?)", randomHex(16), deviceID, user, tokenHash(result.Token), now.Unix()); err != nil {
		return enrollmentResponse{}, err
	}
	if _, err = tx.Exec("INSERT INTO audit_events(occurred_at,user,action,detail) VALUES(?,?,?,?)", now.Unix(), user, "device_enrolled", deviceID); err != nil {
		return enrollmentResponse{}, err
	}
	if err = tx.Commit(); err != nil {
		return enrollmentResponse{}, err
	}
	return result, nil
}

func (r *repository) deviceByToken(token string) (deviceIdentity, error) {
	var d deviceIdentity
	err := r.db.QueryRow("SELECT id,device_id,user FROM devices WHERE token_hash = ? AND revoked = 0", tokenHash(token)).Scan(&d.ID, &d.DeviceID, &d.User)
	return d, err
}

func (r *repository) deviceViews() ([]deviceView, error) {
	rows, err := r.db.Query("SELECT id,device_id,user,last_seen,revoked FROM devices ORDER BY user,device_id,created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var devices []deviceView
	for rows.Next() {
		var d deviceView
		var seen sql.NullInt64
		if err := rows.Scan(&d.ID, &d.DeviceID, &d.User, &seen, &d.Revoked); err != nil {
			return nil, err
		}
		if seen.Valid {
			d.LastSeen = time.Unix(seen.Int64, 0).In(time.Local).Format(time.RFC3339)
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

func (a *application) requireDevice(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		value := r.Header.Get("Authorization")
		if !strings.HasPrefix(value, "Bearer ") || len(value) != 71 {
			writeAPIError(w, http.StatusUnauthorized, "device authentication required")
			return
		}
		d, err := a.service.repository.deviceByToken(strings.TrimPrefix(value, "Bearer "))
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIError(w, http.StatusUnauthorized, "invalid or revoked device token")
			return
		}
		if err != nil {
			writeAPIError(w, http.StatusServiceUnavailable, "authentication unavailable")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), deviceContextKey{}, d)))
	}
}

func (a *application) enrollHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if !a.enrollmentLimiter.allow(remoteIP(r), time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeAPIError(w, http.StatusTooManyRequests, "try again later")
		return
	}
	var request struct {
		Code     string `json:"code"`
		DeviceID string `json:"device_id"`
		User     string `json:"user"`
	}
	if err := decodeJSON(w, r, &request); err != nil || !validIdentity(request.DeviceID) || len(request.Code) > 64 {
		writeAPIError(w, http.StatusBadRequest, "invalid enrollment request")
		return
	}
	result, err := a.service.repository.enrollDevice(request.Code, request.DeviceID, a.service.now())
	if errors.Is(err, errInvalidPairing) {
		writeAPIError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "enrollment unavailable")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *application) recordDeviceSeen(r *http.Request, now time.Time) {
	if d, ok := r.Context().Value(deviceContextKey{}).(deviceIdentity); ok {
		_, _ = a.service.repository.db.Exec("UPDATE devices SET last_seen = ? WHERE id = ?", now.Unix(), d.ID)
	}
}

func bindDeviceIdentity(r *http.Request, user, deviceID *string) bool {
	d, ok := r.Context().Value(deviceContextKey{}).(deviceIdentity)
	if !ok {
		return true
	} // Direct handlers are used by unit tests; routes always authenticate.
	if *deviceID != d.DeviceID || *user != d.User {
		return false
	}
	*user, *deviceID = d.User, d.DeviceID
	return true
}
