package main

import "time"

type heartbeatResult struct {
	Action            string
	DailyTotalSeconds int
	PolicyVersion     int
	RemainingSeconds  int
	ReportedAt        time.Time
}

type screenTimeService struct {
	repository *repository
	now        func() time.Time
}

func newScreenTimeService(repository *repository) *screenTimeService {
	return &screenTimeService{repository: repository, now: time.Now}
}

func (s *screenTimeService) recordHeartbeat(h heartbeat) (heartbeatResult, error) {
	h.ReportedAt = s.now()
	date := h.ReportedAt.In(time.Local).Format("2006-01-02")
	if err := s.repository.addHeartbeat(h); err != nil {
		return heartbeatResult{}, err
	}

	dailyTotal, err := s.dailyTotal(h.User, date)
	if err != nil {
		return heartbeatResult{}, err
	}
	quota, err := s.repository.userQuota(h.User)
	if err != nil {
		return heartbeatResult{}, err
	}
	policyVersion, err := s.repository.userPolicyVersion(h.User)
	if err != nil {
		return heartbeatResult{}, err
	}
	action, remainingSeconds := screenTimeDecision(dailyTotal, quota)
	return heartbeatResult{
		Action:            action,
		DailyTotalSeconds: dailyTotal,
		PolicyVersion:     policyVersion,
		RemainingSeconds:  remainingSeconds,
		ReportedAt:        h.ReportedAt,
	}, nil
}

func (s *screenTimeService) addHeartbeat(h heartbeat) (int, error) {
	date := h.ReportedAt.In(time.Local).Format("2006-01-02")
	if err := s.repository.addHeartbeat(h); err != nil {
		return 0, err
	}
	return s.dailyTotal(h.User, date)
}

func (s *screenTimeService) dailyTotal(user, date string) (int, error) {
	heartbeats, err := s.repository.heartbeatsForUser(user)
	if err != nil {
		return 0, err
	}
	return calculateDailyTotal(heartbeats, date)
}

func (s *screenTimeService) todaysActivities() ([]activity, error) {
	date := today()
	activities, err := s.repository.activities()
	if err != nil {
		return nil, err
	}
	for i := range activities {
		activities[i].TotalSeconds, err = s.dailyTotal(activities[i].User, date)
		if err != nil {
			return nil, err
		}
	}
	return activities, nil
}

func (s *screenTimeService) setUserQuota(user string, quota int) error {
	return s.repository.setUserQuota(user, quota)
}

func today() string {
	return time.Now().Format("2006-01-02")
}
