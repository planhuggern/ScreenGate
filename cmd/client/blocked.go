package main

type blockedState struct {
	blocked       bool
	policyVersion int
}

func (s *blockedState) applyServerAction(action string) {
	s.blocked = action == "lock"
}

func (s *blockedState) updatePolicyVersion(version int) bool {
	changed := s.policyVersion != version
	s.policyVersion = version
	return changed
}

func (s blockedState) lockOnSessionEventWhenHeartbeatFails() bool {
	return s.blocked
}
