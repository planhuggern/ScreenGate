package main

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstallerUsesDownloadOrigin(t *testing.T) {
	for _, tc := range []struct{ download, trusted, want string }{
		{"http://10.0.0.20:8081/downloads/install.ps1", "", "http://10.0.0.20:8081/heartbeat"},
		{"https://screen.example/admin/downloads/install.ps1", "", "https://screen.example/heartbeat"},
		{"http://screen.example/downloads/install.ps1", "https://screen.example", "https://screen.example/heartbeat"},
		{"http://10.0.0.20:8081/downloads/install.ps1", "https://other.example", "http://10.0.0.20:8081/heartbeat"},
		{"http://[::1]:8081/downloads/install.ps1", "", "http://[::1]:8081/heartbeat"},
	} {
		t.Run(tc.download+tc.trusted, func(t *testing.T) {
			t.Setenv("SCREENGATE_TRUSTED_ORIGINS", tc.trusted)
			r := httptest.NewRequest("GET", tc.download, nil)
			r.Header.Set("X-Forwarded-Host", "attacker.example")
			w := httptest.NewRecorder()
			downloadInstallerHandler(w, r)
			body := w.Body.String()
			if w.Code != 200 || !strings.Contains(body, "FromBase64String('"+base64.StdEncoding.EncodeToString([]byte(tc.want))+"')") || strings.Contains(body, "SCREENGATE_SERVER_DEFAULT") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("installer did not embed the expected origin")
			}
		})
	}
}
