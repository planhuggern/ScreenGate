package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

//go:embed cmd/client/install.ps1 cmd/client/uninstall.ps1 cmd/client/update.ps1
var installerFiles embed.FS

func clientBinaryPath() string {
	if path := os.Getenv("CLIENT_BINARY_PATH"); path != "" {
		return path
	}
	if _, err := os.Stat("/client/screengate-client.exe"); err == nil {
		return "/client/screengate-client.exe"
	}
	return "screengate-client.exe"
}

func downloadClientHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Content-Type", "application/vnd.microsoft.portable-executable")
	w.Header().Set("Content-Disposition", "attachment; filename=screengate-client.exe")
	http.ServeFile(w, r, clientBinaryPath())
}

func downloadChecksumHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	checksum, err := clientChecksum()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "%s  screengate-client.exe\n", checksum)
}

func clientChecksum() (string, error) {
	f, err := os.Open(clientBinaryPath())
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func downloadInstallerHandler(w http.ResponseWriter, r *http.Request) {
	downloadScript(w, r, "install.ps1")
}

func downloadUninstallerHandler(w http.ResponseWriter, r *http.Request) {
	downloadScript(w, r, "uninstall.ps1")
}

func downloadScript(w http.ResponseWriter, r *http.Request, name string) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeInstallerScript(w, r, name, "")
}

// Personalized installers are returned directly from the authenticated POST.
// The one-use code never appears in a URL or a cached response.
func writeInstallerScript(w http.ResponseWriter, r *http.Request, name, enrollmentCode string) {
	data, err := installerFiles.ReadFile("cmd/client/" + name)
	if err != nil {
		http.Error(w, "installer unavailable", http.StatusInternalServerError)
		return
	}
	if name == "install.ps1" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		// A configured public origin supplies the external scheme behind a
		// reverse proxy. Do not take an arbitrary forwarded host as the server.
		for _, origin := range configuredTrustedOrigins() {
			u, err := url.Parse(origin)
			if err == nil && strings.EqualFold(u.Host, r.Host) && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") {
				scheme = u.Scheme
				break
			}
		}
		endpoint := (&url.URL{Scheme: scheme, Host: r.Host, Path: "/heartbeat"}).String()
		// Encode request data rather than interpolating it as PowerShell code.
		defaultValue := "([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + base64.StdEncoding.EncodeToString([]byte(endpoint)) + "')))"
		data = bytes.Replace(data, []byte("''<# SCREENGATE_SERVER_DEFAULT #>"), []byte(defaultValue), 1)
		enrollmentDefault := "''"
		if enrollmentCode != "" {
			enrollmentDefault = "([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + base64.StdEncoding.EncodeToString([]byte(enrollmentCode)) + "')))"
		}
		data = bytes.Replace(data, []byte("''<# SCREENGATE_ENROLLMENT_DEFAULT #>"), []byte(enrollmentDefault), 1)
		updater, err := installerFiles.ReadFile("cmd/client/update.ps1")
		if err != nil {
			http.Error(w, "updater unavailable", http.StatusInternalServerError)
			return
		}
		data = bytes.Replace(data, []byte("'SCREENGATE_UPDATER_BASE64'"), []byte("'"+base64.StdEncoding.EncodeToString(updater)+"'"), 1)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	_, _ = w.Write(data)
}
