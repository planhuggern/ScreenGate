package main

// addHistoricalHeartbeat seeds timestamped history for tests. Production goes
// through recordHeartbeat, which uses server time and validates the request.
func (s *screenTimeService) addHistoricalHeartbeat(h heartbeat) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	date := h.ReportedAt.In(s.location).Format("2006-01-02")
	if _, _, err := s.repository.addHeartbeatObserved(h); err != nil {
		return 0, err
	}
	return s.dailyTotal(h.User, date)
}
