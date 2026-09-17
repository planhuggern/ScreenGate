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
	app, seconds := t.app, max(0, int(now.Sub(t.startedAt).Seconds()))
	t.app = ""
	t.startedAt = time.Time{}
	return app, seconds, true
}

func (t *focusTracker) checkpoint(now time.Time) (string, int, bool) {
	if t.app == "" {
		return "", 0, false
	}
	seconds := max(0, int(now.Sub(t.startedAt).Seconds()))
	t.startedAt = now
	return t.app, seconds, true
}
