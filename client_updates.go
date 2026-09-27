package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"os"
	"sync"
)

var updateKeyMu sync.Mutex

// The key lives with the database, so container rebuilds and restored backups
// retain the identity pinned by the installer. Never send the private key.
func (r *repository) clientUpdateKey() (*rsa.PrivateKey, error) {
	updateKeyMu.Lock()
	defer updateKeyMu.Unlock()
	var der []byte
	err := r.db.QueryRow(`SELECT private_key FROM client_update_key WHERE id = 1`).Scan(&der)
	if err == sql.ErrNoRows {
		key, keyErr := rsa.GenerateKey(rand.Reader, 3072)
		if keyErr != nil {
			return nil, keyErr
		}
		if _, err = r.db.Exec(`INSERT OR IGNORE INTO client_update_key (id, private_key) VALUES (1, ?)`, x509.MarshalPKCS1PrivateKey(key)); err != nil {
			return nil, err
		}
		err = r.db.QueryRow(`SELECT private_key FROM client_update_key WHERE id = 1`).Scan(&der)
	}
	if err != nil {
		return nil, err
	}
	return x509.ParsePKCS1PrivateKey(der)
}

type updatePublicKey struct {
	Modulus  string `json:"modulus"`
	Exponent string `json:"exponent"`
}

func (a *application) downloadUpdateKey(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	key, err := a.service.repository.clientUpdateKey()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "update key unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, updatePublicKey{
		Modulus:  base64.StdEncoding.EncodeToString(key.N.Bytes()),
		Exponent: base64.StdEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	})
}

type updateManifest struct {
	Protocol int    `json:"protocol"`
	Nonce    string `json:"nonce"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type signedUpdateManifest struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

func (a *application) downloadUpdateManifest(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	// A fresh challenge prevents replay of an old signed release, without
	// relying on the PC's wall clock or synchronizing version counters.
	nonce := r.URL.Query().Get("nonce")
	decoded, err := hex.DecodeString(nonce)
	if err != nil || len(decoded) != 32 {
		writeAPIError(w, http.StatusBadRequest, "invalid update nonce")
		return
	}
	f, err := os.Open(clientBinaryPath())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, f)
	if err != nil || size < 1024 || size > 128*1024*1024 {
		writeAPIError(w, http.StatusServiceUnavailable, "client unavailable")
		return
	}
	key, err := a.service.repository.clientUpdateKey()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "update key unavailable")
		return
	}
	payload, err := json.Marshal(updateManifest{Protocol: 1, Nonce: nonce, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "manifest unavailable")
		return
	}
	digest := sha256.Sum256(payload)
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "manifest unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, signedUpdateManifest{Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(signature)})
}
