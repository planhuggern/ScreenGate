package main

import "testing"

func TestLockSetsBlocked(t *testing.T) {
	state := blockedState{}
	state.applyServerAction("lock")

	if !state.blocked {
		t.Fatal("lock did not set blocked")
	}
}

func TestAllowClearsBlocked(t *testing.T) {
	state := blockedState{blocked: true}
	state.applyServerAction("allow")

	if state.blocked {
		t.Fatal("allow did not clear blocked")
	}
}

func TestUnlockWhileBlockedLocksWhenHeartbeatFails(t *testing.T) {
	state := blockedState{blocked: true}

	if !state.lockOnSessionEventWhenHeartbeatFails() {
		t.Fatal("blocked session event did not trigger lock")
	}
}

func TestUnlockWhileAllowedDoesNotLockWhenHeartbeatFails(t *testing.T) {
	state := blockedState{}

	if state.lockOnSessionEventWhenHeartbeatFails() {
		t.Fatal("allowed session event triggered lock")
	}
}

func TestBlockedPersistsUntilAllow(t *testing.T) {
	state := blockedState{}
	state.applyServerAction("lock")

	if !state.blocked {
		t.Fatal("blocked state was not retained")
	}
	state.applyServerAction("allow")
	if state.blocked {
		t.Fatal("fresh allow did not clear blocked state")
	}
}

func TestFreshAllowAfterUnlockClearsBlockedState(t *testing.T) {
	state := blockedState{blocked: true}
	state.applyServerAction("allow")

	if state.blocked {
		t.Fatal("fresh allow after unlock did not clear blocked state")
	}
}
