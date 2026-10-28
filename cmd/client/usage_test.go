package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPendingUsageSurvivesRestartAndChangedSession(t *testing.T) {
	now := testMoment()
	state := clientState{Identity: "client", Action: "lock", Reason: "awaiting_server"}
	first, err := state.prepareReport("pc", "child", "active", now)
	if err != nil {
		t.Fatal(err)
	}
	state.apply(response{Action: "allow", UsageAck: first.Usage.ID, PolicyDate: "2026-09-18", QuotaSeconds: 120, RemainingSeconds: 100, LeaseSeconds: 90}, now, now)
	state.account(5, now.Add(5*time.Second))
	report, err := state.prepareReport("pc", "child", "active", now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	state.account(3, now.Add(8*time.Second))
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveState(path, "token", state); err != nil {
		t.Fatal(err)
	}
	restored, err := loadState(path, "client", "token", now.Add(8*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := restored.prepareReport("pc", "child", "locked", now.Add(8*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if changed.HeartbeatID == report.HeartbeatID || changed.Usage.ID != report.Usage.ID || changed.Usage.Intervals[0].End.Sub(changed.Usage.Intervals[0].Start) != 5*time.Second {
		t.Fatal("session change lost or changed the pending usage batch")
	}
	restored.apply(response{Action: "allow", UsageAck: report.Usage.ID, PolicyDate: "2026-09-18", QuotaSeconds: 120, RemainingSeconds: 95, LeaseSeconds: 90}, now.Add(8*time.Second), now.Add(8*time.Second))
	if restored.RemainingSeconds != 92 || restored.PendingUsage != nil {
		t.Fatal("acknowledgement returned locally used time or lost queued use")
	}
	next, err := restored.prepareReport("pc", "child", "locked", now.Add(8*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if next.Usage.ID == report.Usage.ID || len(next.Usage.Intervals) != 1 || next.Usage.Intervals[0].End.Sub(next.Usage.Intervals[0].Start) != 3*time.Second {
		t.Fatal("next batch did not preserve the remaining three seconds")
	}
}

func TestUsageWaitingAcrossMidnightOnlyReducesItsOwnDay(t *testing.T) {
	location, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	until := time.Date(2026, 10, 29, 0, 0, 10, 0, location)
	state := clientState{UsageStarted: true, PendingUsage: &usageBatch{ID: "acked"}, ServerContacted: true}
	state.recordUsage(20, until)
	state.apply(response{Action: "allow", UsageAck: "acked", PolicyDate: "2026-10-29", Timezone: "Europe/Oslo", ServerTime: until, QuotaSeconds: 100, RemainingSeconds: 100}, until, until)
	if state.RemainingSeconds != 90 || state.unacknowledgedUsage("2026-10-28") != 10 {
		t.Fatalf("queued usage crossed day boundary: remaining=%d", state.RemainingSeconds)
	}
}

func TestUsageBacklogIsBatchedWithoutDiscardingIntervals(t *testing.T) {
	state := clientState{UsageStarted: true}
	now := testMoment()
	for i := 0; i < 40; i++ {
		state.recordUsage(1, now.Add(time.Duration(2*i)*time.Second))
	}
	if err := state.prepareUsage(); err != nil {
		t.Fatal(err)
	}
	if len(state.PendingUsage.Intervals) != 32 || len(state.UnreportedUsage) != 8 {
		t.Fatal("batch limit discarded backlog")
	}
	first := state.PendingUsage.ID
	if err := state.prepareUsage(); err != nil || state.PendingUsage.ID != first {
		t.Fatal("pending batch changed before acknowledgement")
	}
}

func TestOversizedStateDoesNotReplaceLastSavedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := clientState{Identity: "client", Action: "lock", UpdatedAt: testMoment()}
	if err := saveState(path, "token", state); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state.Reason = strings.Repeat("x", maxStateBytes)
	if err := saveState(path, "token", state); err == nil {
		t.Fatal("oversized state was written despite the load size limit")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("oversized write replaced the last readable file")
	}
}

func TestFirstUsageAcknowledgementCalibratesUpgradedClientClock(t *testing.T) {
	serverNow := testMoment()
	pcNow := serverNow.Add(time.Hour)
	state := clientState{ServerContacted: true, PolicyDate: "2026-09-18", QuotaSeconds: 120, RemainingSeconds: 100}
	first, err := state.prepareReport("pc", "child", "active", pcNow)
	if err != nil {
		t.Fatal(err)
	}
	state.account(2, pcNow.Add(2*time.Second))
	state.apply(response{Action: "allow", UsageAck: first.Usage.ID, PolicyDate: "2026-09-18", Timezone: "UTC", ServerTime: serverNow, QuotaSeconds: 120, RemainingSeconds: 100}, pcNow, pcNow.Add(2*time.Second))
	if len(state.UnreportedUsage) != 1 || !state.UnreportedUsage[0].Start.Equal(serverNow) || !state.UnreportedUsage[0].End.Equal(serverNow.Add(2*time.Second)) || state.RemainingSeconds != 98 {
		t.Fatal("an upgraded client's PC clock leaked into its usage timestamps")
	}
}
