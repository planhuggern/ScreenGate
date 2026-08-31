package main

import (
	"testing"
	"time"
)

func TestFocusChangeCreatesEvent(t *testing.T) {
	tracker := focusTracker{}
	startedAt := time.Date(2026, 8, 31, 18, 2, 11, 0, time.UTC)
	tracker.observe("roblox.exe", startedAt)

	app, seconds, changed := tracker.observe("chrome.exe", startedAt.Add(332*time.Second))

	if !changed || app != "roblox.exe" || seconds != 332 {
		t.Fatalf("event = %q, %d, %t", app, seconds, changed)
	}
}

func TestSameFocusAppDoesNotCreateEvent(t *testing.T) {
	tracker := focusTracker{}
	startedAt := time.Date(2026, 8, 31, 18, 2, 11, 0, time.UTC)
	tracker.observe("roblox.exe", startedAt)

	_, _, changed := tracker.observe("roblox.exe", startedAt.Add(time.Minute))

	if changed {
		t.Fatal("same app created an event")
	}
}
