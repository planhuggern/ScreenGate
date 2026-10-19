package main

import (
	"encoding/binary"
	"testing"
	"time"
)

func sessionInfoFixture(level, sessionID, state, flags uint32) []byte {
	info := make([]byte, 232) // sizeof(WTSINFOEXW)
	binary.LittleEndian.PutUint32(info[0:4], level)
	binary.LittleEndian.PutUint32(info[8:12], sessionID)
	binary.LittleEndian.PutUint32(info[12:16], state)
	binary.LittleEndian.PutUint32(info[16:20], flags)
	return info
}

func TestWTSSessionUnlocked(t *testing.T) {
	for _, tt := range []struct {
		name string
		info []byte
		want bool
	}{
		{"unlocked console", sessionInfoFixture(1, 7, 0, 1), true},
		{"locked console remains WTSActive", sessionInfoFixture(1, 7, 0, 0), false},
		{"disconnected remote session", sessionInfoFixture(1, 7, 4, 1), false},
		{"different user session", sessionInfoFixture(1, 8, 0, 1), false},
		{"unknown lock state", sessionInfoFixture(1, 7, 0, 0xffffffff), false},
		{"unknown level", sessionInfoFixture(2, 7, 0, 1), false},
		{"missing response", nil, false},
		{"truncated response", sessionInfoFixture(1, 7, 0, 1)[:19], false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := wtsSessionUnlocked(tt.info, 7); got != tt.want {
				t.Fatalf("unlocked=%t, want %t", got, tt.want)
			}
		})
	}
}

func TestLockedWTSActiveSessionStopsActivityMeter(t *testing.T) {
	now := testMoment()
	meter := activityMeter{}
	meter.update(wtsSessionUnlocked(sessionInfoFixture(1, 7, 0, 1), 7), now)
	// The poll that first sees the lock may finish the preceding second.
	meter.update(wtsSessionUnlocked(sessionInfoFixture(1, 7, 0, 0), 7), now.Add(time.Second))
	for second := 2; second <= 120; second++ {
		if got := meter.update(wtsSessionUnlocked(sessionInfoFixture(1, 7, 0, 0), 7), now.Add(time.Duration(second)*time.Second)); got != 0 {
			t.Fatalf("locked second %d billed %d seconds", second, got)
		}
	}
	if got := meter.update(true, now.Add(121*time.Second)); got != 0 {
		t.Fatalf("unlock billed locked interval: %d", got)
	}
	if got := meter.update(true, now.Add(122*time.Second)); got != 1 {
		t.Fatalf("unlocked interval billed %d, want 1", got)
	}
}
