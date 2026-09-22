package main

import "time"

type hourlyUsage struct {
	Start    time.Time
	End      time.Time
	Duration time.Duration
}

// The date is explicit so the same aggregation can serve historical days later.
// Walking real hours preserves both occurrences of an hour when clocks go back.
func calculateHourlyUsage(heartbeats []recordedHeartbeat, date string, location *time.Location) ([]hourlyUsage, error) {
	start, err := time.ParseInLocation("2006-01-02", date, location)
	if err != nil {
		return nil, err
	}
	end := start.AddDate(0, 0, 1)
	hours := make([]hourlyUsage, 0, 25)
	for cursor := start; cursor.Before(end); {
		next := cursor.Add(time.Hour)
		if next.After(end) {
			next = end
		}
		hours = append(hours, hourlyUsage{Start: cursor, End: next})
		cursor = next
	}
	forEachUsageInterval(heartbeats, func(from, to time.Time) {
		for i := range hours {
			left, right := from, to
			if left.Before(hours[i].Start) {
				left = hours[i].Start
			}
			if right.After(hours[i].End) {
				right = hours[i].End
			}
			if right.After(left) {
				hours[i].Duration += right.Sub(left)
			}
		}
	})
	return hours, nil
}
