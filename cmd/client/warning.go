package main

type screenTimeWarning struct {
	threshold int
	message   string
	color     string
}

var screenTimeWarnings = []screenTimeWarning{
	{threshold: 900, message: "15 minutter igjen", color: "#229A5A"},
	{threshold: 600, message: "10 minutter igjen", color: "#D39E00"},
	{threshold: 300, message: "5 minutter igjen", color: "#E67E22"},
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
