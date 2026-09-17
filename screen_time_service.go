package main

import (
	"errors"
	"sync"
	"time"
)

var errInvalidHeartbeat = errors.New("invalid heartbeat")

type heartbeatResult struct {
	Action            string
	DailyTotalSeconds int
	PolicyVersion     int
	RemainingSeconds  int
	ReportedAt        time.Time
	Reason            string
	QuotaSeconds      int
	Unlimited         bool
	NextAllowedAt     time.Time
	NextTransitionAt  time.Time
	LeaseSeconds      int
	ServerTime        time.Time
	PolicyDate        string
}

type screenTimeService struct {
	repository *repository
	now        func() time.Time
	location   *time.Location
	mu         sync.Mutex
}

func newScreenTimeService(repository *repository) *screenTimeService {
	return &screenTimeService{repository: repository, now: time.Now, location: time.Local}
}

func (s *screenTimeService) recordHeartbeat(h heartbeat) (heartbeatResult, error) {
	if !validPolicyUser(h.User) || h.DeviceID == "" || len(h.DeviceID) > 200 || len(h.HeartbeatID) > 200 || h.ActiveSeconds < 0 || h.ActiveSeconds > 86400 || (h.SessionState != "" && h.SessionState != "active" && h.SessionState != "idle" && h.SessionState != "locked") {
		return heartbeatResult{}, errInvalidHeartbeat
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h.ReportedAt = s.now().In(s.location)
	date := h.ReportedAt.Format("2006-01-02")
	if h.ActivityDate != "" {
		activityDay, err := time.ParseInLocation("2006-01-02", h.ActivityDate, s.location)
		if err != nil || activityDay.Year() < 1970 || h.ActivityDate > date {
			return heartbeatResult{}, errInvalidHeartbeat
		}
	}
	if err := s.repository.addHeartbeatInLocation(h, s.location); err != nil {
		return heartbeatResult{}, err
	}

	dailyTotal, err := s.dailyTotal(h.User, date)
	if err != nil {
		return heartbeatResult{}, err
	}
	policy, err := s.repository.userPolicy(h.User, date)
	if err != nil {
		return heartbeatResult{}, err
	}
	decision := evaluatePolicy(policy, dailyTotal, h.ReportedAt)
	return heartbeatResult{
		Action:            decision.Action,
		DailyTotalSeconds: dailyTotal,
		PolicyVersion:     policy.PolicyVersion,
		RemainingSeconds:  decision.RemainingSeconds,
		ReportedAt:        h.ReportedAt,
		Reason:            decision.Reason,
		QuotaSeconds:      decision.QuotaSeconds,
		Unlimited:         decision.Unlimited,
		NextAllowedAt:     decision.NextAllowedAt,
		NextTransitionAt:  decision.NextTransitionAt,
		LeaseSeconds:      decision.LeaseSeconds,
		ServerTime:        h.ReportedAt,
		PolicyDate:        date,
	}, nil
}

func (s *screenTimeService) addHeartbeat(h heartbeat) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	date := h.ReportedAt.In(s.location).Format("2006-01-02")
	if err := s.repository.addHeartbeatInLocation(h, s.location); err != nil {
		return 0, err
	}
	return s.dailyTotal(h.User, date)
}

func (s *screenTimeService) dailyTotal(user, date string) (int, error) {
	start, err := time.ParseInLocation("2006-01-02", date, s.location)
	if err != nil {
		return 0, err
	}
	heartbeats, err := s.repository.heartbeatsForUserRange(user, start, start.AddDate(0, 0, 1))
	if err != nil {
		return 0, err
	}
	return calculateDailyTotalInLocation(heartbeats, date, s.location)
}

func (s *screenTimeService) todaysActivities() ([]activity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().In(s.location)
	date := now.Format("2006-01-02")
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.location)
	activities, err := s.repository.activities()
	if err != nil {
		return nil, err
	}
	for i := range activities {
		item := &activities[i]
		heartbeats, err := s.repository.heartbeatsForUserRange(item.User, start.AddDate(0, 0, -6), start.AddDate(0, 0, 1))
		if err != nil {
			return nil, err
		}
		for day := -6; day <= 0; day++ {
			dayDate := start.AddDate(0, 0, day).Format("2006-01-02")
			total, err := calculateDailyTotalInLocation(heartbeats, dayDate, s.location)
			if err != nil {
				return nil, err
			}
			item.History = append(item.History, dailyUsage{Date: dayDate, TotalSeconds: total})
			if day == 0 {
				item.TotalSeconds = total
			}
		}
		policy, err := s.repository.userPolicy(item.User, date)
		if err != nil {
			return nil, err
		}
		decision := evaluatePolicy(policy, item.TotalSeconds, now)
		item.BaseQuotaSeconds = policy.DailyQuotaSeconds
		item.BonusSeconds = policy.BonusSeconds
		item.QuotaSeconds = decision.QuotaSeconds
		item.RemainingSeconds = decision.RemainingSeconds
		item.Unlimited = decision.Unlimited
		item.Paused = policy.Paused
		item.Action, item.Reason = decision.Action, decision.Reason
		item.NextAllowedAt = decision.NextAllowedAt
		item.Weekdays = policy.Weekdays
		item.Devices, err = s.repository.devicesForUser(item.User, now)
		if err != nil {
			return nil, err
		}
		for _, device := range item.Devices {
			item.Online = item.Online || device.Online
		}
	}
	return activities, nil
}

func (s *screenTimeService) setUserQuota(user string, quota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repository.setUserQuota(user, quota)
}

func (s *screenTimeService) userPolicy(user string) (userPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repository.userPolicy(user, s.now().In(s.location).Format("2006-01-02"))
}

func (s *screenTimeService) setWeekdayPolicies(user string, days []weekdayPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repository.setWeekdayPolicies(user, days)
}

func (s *screenTimeService) setUserPaused(user string, paused bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repository.setUserPaused(user, paused)
}

func (s *screenTimeService) addBonusMinutes(user string, minutes int) error {
	if minutes < 1 || minutes > 1440 {
		return errInvalidPolicy
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repository.changeBonus(user, s.now().In(s.location).Format("2006-01-02"), minutes*60, false)
}

func (s *screenTimeService) clearBonus(user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repository.changeBonus(user, s.now().In(s.location).Format("2006-01-02"), 0, true)
}

func (s *screenTimeService) today() string {
	return s.now().In(s.location).Format("2006-01-02")
}

func today() string {
	return time.Now().Format("2006-01-02")
}
