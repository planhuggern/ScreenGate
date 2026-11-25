package main

import (
	"testing"
	"time"
)

func TestCountdownDisplaysEffectiveTime(t *testing.T) {
	now := testMoment()
	state := clientState{ServerContacted: true, QuotaSeconds: 7200, RemainingSeconds: 3661}
	if got := state.countdownText(now, true); got != "1:01:01" {
		t.Fatalf("quota countdown = %q", got)
	}
	state.ScheduledLockAt = now.Add(74 * time.Second)
	if got := state.countdownText(now, true); got != "01:14" {
		t.Fatalf("schedule countdown = %q", got)
	}
	state.QuotaSeconds = 0
	if got := state.countdownText(now.Add(time.Second), true); got != "01:13" {
		t.Fatalf("schedule countdown with unlimited quota = %q", got)
	}
	if got := state.countdownText(now, false); got != "00:00" {
		t.Fatalf("blocked countdown = %q", got)
	}
	state.ScheduledLockAt = time.Time{}
	if got := state.countdownText(now, true); got != "Ubegrenset" {
		t.Fatalf("unlimited countdown = %q", got)
	}
	state.invalidate("authentication_failed")
	if got := state.countdownText(now, false); got != "--:--" {
		t.Fatalf("invalid countdown = %q", got)
	}
}

func TestWarningTriggersWhenThresholdIsCrossed(t *testing.T) {
	state := warningState{}
	state.observe(1, 901)

	warning, _ := state.observe(1, 900)
	if warning == nil || warning.threshold != 900 {
		t.Fatalf("warning = %#v, want 15 minute warning", warning)
	}
	if warning, _ := state.observe(1, 899); warning != nil {
		t.Fatalf("warning repeated: %#v", warning)
	}
}

func TestWarningDoesNotFireForPassedThresholdsAtStartup(t *testing.T) {
	state := warningState{}
	if warning, _ := state.observe(1, 420); warning != nil {
		t.Fatalf("startup warning = %#v", warning)
	}
	warning, _ := state.observe(1, 300)
	if warning == nil || warning.threshold != 300 {
		t.Fatalf("warning = %#v, want 5 minute warning", warning)
	}
}

func TestWarningDoesNotFireWhenAllThresholdsAlreadyPassed(t *testing.T) {
	state := warningState{}
	if warning, _ := state.observe(1, 250); warning != nil {
		t.Fatalf("startup warning = %#v", warning)
	}
	if warning, _ := state.observe(1, 200); warning != nil {
		t.Fatalf("warning = %#v", warning)
	}
}

func TestPolicyVersionResetsWarningState(t *testing.T) {
	state := warningState{}
	state.observe(1, 901)
	state.observe(1, 900)
	if warning, changed := state.observe(2, 901); warning != nil || !changed {
		t.Fatalf("policy reset = %#v, %t", warning, changed)
	}
	warning, _ := state.observe(2, 900)
	if warning == nil || warning.threshold != 900 {
		t.Fatalf("warning = %#v after policy reset", warning)
	}
}
