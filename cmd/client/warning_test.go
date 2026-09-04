package main

import "testing"

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
