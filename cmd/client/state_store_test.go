package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadLegacyStateFilePreservesQuotaAndPendingReport(t *testing.T) {
	// Fixed bytes and MAC, written independently of saveState. This fixture
	// represents an existing file from before ServerContacted was introduced.
	fixture := filepath.Join("testdata", "state-before-server-contacted.json")
	now := testMoment().Add(5 * time.Second)
	state, err := loadState(fixture, "legacy-client", "fixture-token", now)
	if err != nil {
		t.Fatal(err)
	}
	if !state.ServerContacted || !state.allowed(now) {
		t.Fatal("legacy authorization was not restored")
	}
	if state.RemainingSeconds != 30 || state.PendingSeconds != 4 || state.PendingDate != "2026-09-18" {
		t.Fatal("loading changed the saved quota or unreported use")
	}
	if !state.LeaseExpiresAt.Equal(testMoment().Add(time.Minute)) {
		t.Fatal("loading extended the saved expiry")
	}
	if state.Report == nil || state.Report.HeartbeatID != "pending-report" || state.Report.ActiveSeconds != 25 {
		t.Fatal("loading lost the pending report or its retry ID")
	}

	// Rewriting an old file in the existing format must still preserve it.
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, "fixture-token", state); err != nil {
		t.Fatal(err)
	}
	restored, err := loadState(path, "legacy-client", "fixture-token", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if restored.RemainingSeconds != 30 || restored.PendingSeconds != 4 || restored.Report == nil || restored.Report.HeartbeatID != "pending-report" {
		t.Fatal("rewriting the legacy state lost quota or its pending report")
	}
}

func TestUnreadableStateContentRetainsClosedFallback(t *testing.T) {
	for _, tt := range []struct {
		name, content string
	}{
		{"truncated JSON", `{"state":`},
		{"oversized file", strings.Repeat(" ", maxStateBytes+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
				t.Fatal(err)
			}
			state, err := loadState(path, "client", "token", testMoment())
			if err == nil || !state.ServerContacted || state.allowed(testMoment()) {
				t.Fatal("invalid saved state was treated as a fresh installation")
			}
		})
	}
}
