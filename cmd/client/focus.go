package main

import "time"

type focusTracker struct {
	app       string
	startedAt time.Time
}

func (t *focusTracker) observe(app string, now time.Time) (string, int, bool) {
	if app == "" {
		return "", 0, false
	}
	if t.app == "" {
		t.app = app
		t.startedAt = now
		return "", 0, false
	}
	if app == t.app {
		return "", 0, false
	}

	previousApp := t.app
	activeSeconds := int(now.Sub(t.startedAt).Seconds())
	t.app = app
	t.startedAt = now
	return previousApp, activeSeconds, true
}

func (t *focusTracker) finish(now time.Time) (string, int, bool) {
	if t.app == "" {
		return "", 0, false
	}
	return t.app, int(now.Sub(t.startedAt).Seconds()), true
}
