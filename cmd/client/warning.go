package main

import (
	"fmt"
	"time"
)

func (s clientState) countdownText(now time.Time, allowed bool) string {
	switch {
	case !s.ServerContacted || s.LocalBlockReason != "":
		return "--:--"
	case !allowed:
		return "00:00"
	case s.QuotaSeconds == 0 && s.ScheduledLockAt.IsZero():
		return "Ubegrenset"
	}
	seconds := max(0, s.warningRemaining(now))
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

type screenTimeWarning struct {
	threshold int
	message   string
	color     string
}

var screenTimeWarnings = []screenTimeWarning{
	{threshold: 1800, message: "30 minutter igjen", color: "#229A5A"},
	{threshold: 1200, message: "20 minutter igjen", color: "#229A5A"},
	{threshold: 900, message: "15 minutter igjen", color: "#229A5A"},
	{threshold: 600, message: "10 minutter igjen", color: "#D39E00"},
	{threshold: 300, message: "5 minutter igjen", color: "#E67E22"},
	{threshold: 60, message: "1 minutt igjen – lagre arbeidet ditt", color: "#C64B37"},
	{threshold: 30, message: "30 sekunder igjen", color: "#B33030"},
}

type warningState struct {
	initialized       bool
	policyVersion     int
	previousRemaining int
	shown             map[int]bool
}

func (s *warningState) observe(policyVersion, remainingSeconds int) (*screenTimeWarning, bool) {
	if !s.initialized || s.policyVersion != policyVersion {
		s.initialized = true
		s.policyVersion = policyVersion
		s.previousRemaining = remainingSeconds
		s.shown = make(map[int]bool)
		for _, warning := range screenTimeWarnings {
			if remainingSeconds <= warning.threshold {
				s.shown[warning.threshold] = true
			}
		}
		return nil, true
	}

	defer func() { s.previousRemaining = remainingSeconds }()
	if remainingSeconds <= 0 {
		return nil, false
	}
	for index := len(screenTimeWarnings) - 1; index >= 0; index-- {
		warning := screenTimeWarnings[index]
		if !s.shown[warning.threshold] && s.previousRemaining > warning.threshold && remainingSeconds <= warning.threshold {
			s.shown[warning.threshold] = true
			return &warning, false
		}
	}
	return nil, false
}
