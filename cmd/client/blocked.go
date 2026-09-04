package main

type blockedState struct {
	blocked bool
}

func (s *blockedState) applyServerAction(action string) {
	s.blocked = action == "lock"
}

func (s blockedState) lockOnSessionEventWhenHeartbeatFails() bool {
	return s.blocked
}
