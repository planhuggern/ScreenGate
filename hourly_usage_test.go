package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestHourlyUsageSplitsIntervalsAndMatchesDailyAccounting(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 9, 22, 0, 0, 0, 0, loc)
	credit := int64(120000)
	samples := []recordedHeartbeat{
		{deviceID: "a", reportedAt: day.Add(10*time.Hour + time.Minute), creditedMilliseconds: &credit},
		{deviceID: "b", reportedAt: day.Add(10*time.Hour + time.Minute), creditedMilliseconds: &credit},
		// A late report belongs at its accounting time, not its arrival time.
		{deviceID: "c", reportedAt: day.AddDate(0, 0, 5), creditedUntil: day.Add(time.Minute), creditedMilliseconds: &credit},
		{deviceID: "legacy", reportedAt: day.Add(11*time.Hour - 30*time.Second)},
		{deviceID: "legacy", reportedAt: day.Add(11*time.Hour + 30*time.Second)},
		// A long legacy gap must not create activity.
		{deviceID: "legacy", reportedAt: day.Add(15 * time.Hour)},
	}
	hours, err := calculateHourlyUsage(samples, "2026-09-22", loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(hours) != 24 || hours[0].Duration != time.Minute || hours[9].Duration != 2*time.Minute || hours[10].Duration != 150*time.Second || hours[11].Duration != 30*time.Second {
		t.Fatalf("unexpected hourly usage: %+v", hours)
	}
	var sum time.Duration
	for _, hour := range hours {
		sum += hour.Duration
	}
	total, err := calculateDailyTotalInLocation(samples, "2026-09-22", loc)
	if err != nil || int(sum/time.Second) != total || total != 360 {
		t.Fatalf("sum=%v total=%d err=%v", sum, total, err)
	}
}

func TestHourlyUsageDaylightSavingAndEmptyDays(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		date  string
		count int
	}{{"2026-03-29", 23}, {"2026-10-25", 25}, {"2026-09-22", 24}} {
		hours, err := calculateHourlyUsage(nil, tc.date, loc)
		if err != nil || len(hours) != tc.count {
			t.Fatalf("%s: hours=%d err=%v", tc.date, len(hours), err)
		}
		chart := buildHourlyChart(hours)
		if !chart.Empty || chart.MaximumMinutes != 60 {
			t.Fatalf("empty chart=%+v", chart)
		}
		if tc.count == 25 && chart.Bars[2].Label == chart.Bars[3].Label {
			t.Fatal("repeated hour labels must distinguish offsets")
		}
		credit := int64(time.Hour / time.Millisecond)
		samples := make([]recordedHeartbeat, 0, len(hours))
		for _, hour := range hours {
			samples = append(samples, recordedHeartbeat{deviceID: "pc", reportedAt: hour.End, creditedMilliseconds: &credit})
		}
		filled, err := calculateHourlyUsage(samples, tc.date, loc)
		if err != nil {
			t.Fatal(err)
		}
		for _, hour := range filled {
			if hour.Duration != time.Hour {
				t.Fatalf("%s: hour=%+v", tc.date, hour)
			}
		}
		if !hours[len(hours)-1].End.Equal(hours[0].Start.AddDate(0, 0, 1)) {
			t.Fatal("day must end at local midnight")
		}
	}
	if _, err := calculateHourlyUsage(nil, "invalid", loc); err == nil {
		t.Fatal("invalid date accepted")
	}
}

func TestDashboardHourlyUsagePerUser(t *testing.T) {
	app := testApplication(t)
	s := app.service
	s.location = time.UTC
	now := time.Date(2026, 9, 22, 10, 0, 30, 0, time.UTC)
	s.now = func() time.Time { return now }
	for _, user := range []string{"A", "B"} {
		if err := s.setUserQuota(user, 3600); err != nil {
			t.Fatal(err)
		}
	}
	for _, at := range []time.Time{now.Add(-time.Minute), now} {
		if _, err := s.addHeartbeat(heartbeat{User: "A", DeviceID: "pc", ReportedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	activities, err := s.todaysActivities()
	if err != nil {
		t.Fatal(err)
	}
	model := dashboard{Date: "2026-09-22", Activities: activities}
	users := model.Users()
	if len(users) != 2 || len(users[0].HourlyChart.Bars) != 24 || users[0].HourlyChart.Bars[9].Minutes != "0,5" || users[0].HourlyChart.Empty || !users[1].HourlyChart.Empty {
		t.Fatalf("users=%+v", users)
	}
	var page bytes.Buffer
	if err := dashboardTemplate.Execute(&page, model); err != nil {
		t.Fatal(err)
	}
	if strings.Count(page.String(), `class="hourly-column"`) != 48 || strings.Contains(page.String(), "ZgotmplZ") || !strings.Contains(page.String(), "Ingen registrert skjermtid i dag.") {
		t.Fatal("hourly charts missing or invalid")
	}
}

func TestHourlyChartScalesConcurrentUsage(t *testing.T) {
	chart := buildHourlyChart([]hourlyUsage{{Start: time.Now(), Duration: 90 * time.Minute}})
	if chart.MaximumMinutes != 90 || chart.Bars[0].Height != 100 || chart.Bars[0].Minutes != "90,0" {
		t.Fatalf("chart=%+v", chart)
	}
}
