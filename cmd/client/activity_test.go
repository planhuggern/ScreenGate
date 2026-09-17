package main

import (
	"testing"
	"time"
)

func TestActivityDoesNotBillStartupLockedIdleOrSleep(t *testing.T) {
	now := testMoment()
	meter := activityMeter{}
	if got := meter.update(true, now); got != 0 {
		t.Fatalf("startup billed %d", got)
	}
	if got := meter.update(true, now.Add(time.Second)); got != 1 {
		t.Fatalf("active billed %d", got)
	}
	if got := meter.update(false, now.Add(2*time.Second)); got != 1 {
		t.Fatalf("transition billed %d", got)
	}
	if got := meter.update(false, now.Add(3*time.Second)); got != 0 {
		t.Fatalf("inactive billed %d", got)
	}
	if got := meter.update(true, now.Add(time.Hour)); got != 0 {
		t.Fatalf("resume billed %d", got)
	}
	if got := meter.update(true, now.Add(2*time.Hour)); got != 0 {
		t.Fatalf("sleep billed %d", got)
	}
}

func TestActivityPreservesSubsecondUse(t *testing.T) {
	now := testMoment()
	meter := activityMeter{}
	meter.update(true, now)
	if got := meter.update(true, now.Add(600*time.Millisecond)); got != 0 {
		t.Fatalf("got %d", got)
	}
	if got := meter.update(true, now.Add(1200*time.Millisecond)); got != 1 {
		t.Fatalf("got %d", got)
	}
}
