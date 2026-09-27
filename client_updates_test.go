package main

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignedClientUpdateAndPersistentKey(t *testing.T) {
	a := testApplication(t)
	binary := append([]byte("MZ"), bytes.Repeat([]byte{42}, 2048)...)
	path := filepath.Join(t.TempDir(), "client.exe")
	if err := os.WriteFile(path, binary, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLIENT_BINARY_PATH", path)
	nonce := strings.Repeat("a1", 32)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/downloads/update.json?nonce="+nonce, nil))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d: %s", w.Code, w.Body)
	}
	var envelope signedUpdateManifest
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil {
		t.Fatal(err)
	}
	key, err := a.service.repository.clientUpdateKey()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatal(err)
	}
	payload[0] ^= 1
	tampered := sha256.Sum256(payload)
	if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, tampered[:], signature) == nil {
		t.Fatal("tampered manifest accepted")
	}
	payload[0] ^= 1
	var manifest updateManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(binary)
	if manifest.Protocol != 1 || manifest.Nonce != nonce || manifest.Size != int64(len(binary)) || manifest.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("manifest=%+v", manifest)
	}
	// A new application uses the existing database key, as on server restart.
	restarted := newApplication(a.service.repository)
	keyResponse := httptest.NewRecorder()
	restarted.routes().ServeHTTP(keyResponse, httptest.NewRequest("GET", "/downloads/update-key.json", nil))
	var public updatePublicKey
	if err := json.Unmarshal(keyResponse.Body.Bytes(), &public); err != nil {
		t.Fatal(err)
	}
	if public.Modulus != base64.StdEncoding.EncodeToString(key.N.Bytes()) || public.Exponent != "AQAB" {
		t.Fatal("server key changed")
	}
	if strings.Contains(keyResponse.Body.String(), "private") {
		t.Fatal("private key exposed")
	}
}

func TestUpdateManifestRejectsMissingNonceAndUnavailableClient(t *testing.T) {
	a := testApplication(t)
	t.Setenv("CLIENT_BINARY_PATH", filepath.Join(t.TempDir(), "missing.exe"))
	for _, tc := range []struct {
		method, query string
		status        int
	}{
		{"POST", "", 405}, {"GET", "", 400}, {"GET", "?nonce=bad", 400},
		{"GET", "?nonce=" + strings.Repeat("ab", 32), 404},
	} {
		w := httptest.NewRecorder()
		a.downloadUpdateManifest(w, httptest.NewRequest(tc.method, "/downloads/update.json"+tc.query, nil))
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d", tc.method, tc.query, w.Code)
		}
	}
}

func TestInstallerEmbedsUpdater(t *testing.T) {
	w := httptest.NewRecorder()
	downloadInstallerHandler(w, httptest.NewRequest("GET", "http://localhost/downloads/install.ps1", nil))
	updater, err := installerFiles.ReadFile("cmd/client/update.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Body.String(), "'"+base64.StdEncoding.EncodeToString(updater)+"'") || strings.Contains(w.Body.String(), "'SCREENGATE_UPDATER_BASE64'") {
		t.Fatal("installer did not embed updater")
	}
}
