package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientStartupConfigurationPrecedence(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "client.json")
	tokenPath := filepath.Join(dir, "device-token.txt")
	if err := os.WriteFile(configPath, []byte(`{"server":"https://installed.test/heartbeat","user":"installed-user","device_id":"installed-pc","token":"installed-token","enable_locking":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte(" file-token\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCREENGATE_DEVICE_TOKEN", " environment-token ")

	tests := []struct {
		name string
		args []string
		want clientConfig
	}{
		{
			name: "environment supplies token without configuration file",
			args: []string{"-server", "https://manual.test/heartbeat", "-user", "child", "-device-id", "pc"},
			want: clientConfig{Server: "https://manual.test/heartbeat", User: "child", DeviceID: "pc", Token: "environment-token"},
		},
		{
			name: "installation configuration replaces environment",
			args: []string{"-config", configPath},
			want: clientConfig{Server: "https://installed.test/heartbeat", User: "installed-user", DeviceID: "installed-pc", Token: "installed-token", EnableLocking: true},
		},
		{
			name: "explicit arguments override installation values",
			args: []string{"-config", configPath, "-server", "https://manual.test/heartbeat", "-user", "child", "-device-id", "pc", "-token", " argument-token "},
			want: clientConfig{Server: "https://manual.test/heartbeat", User: "child", DeviceID: "pc", Token: "argument-token", EnableLocking: true},
		},
		{
			name: "token file overrides token argument",
			args: []string{"-config", configPath, "-token", "argument-token", "-token-file", tokenPath},
			want: clientConfig{Server: "https://installed.test/heartbeat", User: "installed-user", DeviceID: "installed-pc", Token: "file-token", EnableLocking: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options, err := readClientOptions(flag.NewFlagSet("client-test", flag.ContinueOnError), tt.args)
			if err != nil {
				t.Fatal(err)
			}
			got, err := loadClientConfig(options)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				// Configuration includes credentials; never dump it on failure.
				t.Fatal("resolved settings differ from the expected source precedence")
			}
		})
	}
}

func TestClientStartupRejectsUnusableSettings(t *testing.T) {
	dir := t.TempDir()
	missingToken := filepath.Join(dir, "without-token.json")
	if err := os.WriteFile(missingToken, []byte(`{"server":"https://installed.test/heartbeat","user":"child","device_id":"pc"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCREENGATE_DEVICE_TOKEN", "environment-token")
	base := []string{"-server", "https://manual.test/heartbeat", "-user", "child", "-device-id", "pc"}
	for _, tt := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"configuration does not fall back to environment token", []string{"-config", missingToken}, "device token missing"},
		{"unreadable token file does not fall back to argument", []string{"-token", "argument-token", "-token-file", filepath.Join(dir, "missing.txt")}, "cannot read device token"},
		{"blank explicit token clears environment token", []string{"-token", " "}, "device token missing"},
		{"invalid server address", []string{"-server", "https://example.test/admin"}, "server URL must end in /heartbeat"},
		{"negative inactivity cutoff", []string{"-idle-timeout", "-1s"}, "idle-timeout cannot be negative"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append(append([]string{}, base...), tt.args...)
			options, err := readClientOptions(flag.NewFlagSet("client-test", flag.ContinueOnError), args)
			if err != nil {
				t.Fatal(err)
			}
			_, err = loadClientConfig(options)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error=%v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestClientStartupLockingSelection(t *testing.T) {
	for _, tt := range []struct {
		name       string
		configured bool
		args       []string
		want       bool
	}{
		{"default is test mode", false, nil, false},
		{"installation enables locking", true, nil, true},
		{"argument enables locking", false, []string{"-enable-locking"}, true},
		{"test mode overrides installation", true, []string{"-test-mode"}, false},
		{"test mode overrides locking argument", false, []string{"-enable-locking", "-test-mode"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			options, err := readClientOptions(flag.NewFlagSet("client-test", flag.ContinueOnError), tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if got := options.lockingEnabled(clientConfig{EnableLocking: tt.configured}); got != tt.want {
				t.Fatalf("locking=%t want=%t", got, tt.want)
			}
		})
	}
}
