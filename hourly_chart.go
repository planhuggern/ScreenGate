package main

import (
	"fmt"
	"strings"
	"time"
)

type hourlyChart struct {
	Date           string
	Bars           []hourlyBar
	MaximumMinutes int
	Empty          bool
}

type hourlyBar struct {
	Label   string
	Range   string
	Minutes string
	Height  float64
}

func buildHourlyChart(hours []hourlyUsage) hourlyChart {
	chart := hourlyChart{MaximumMinutes: 60, Empty: true}
	counts := make(map[string]int)
	for _, hour := range hours {
		counts[hour.Start.Format("15")]++
		// Concurrent devices can legitimately contribute more than 60 minutes.
		minutes := int((hour.Duration + time.Minute - 1) / time.Minute)
		chart.MaximumMinutes = max(chart.MaximumMinutes, ((minutes+29)/30)*30)
	}
	for _, hour := range hours {
		label := hour.Start.Format("15")
		if counts[label] > 1 {
			label = hour.Start.Format("15 MST")
		}
		endLabel := hour.End.Format("15:04 MST")
		if hour.End.Format("2006-01-02") != hour.Start.Format("2006-01-02") {
			endLabel = "24:00"
		}
		minutes := "0"
		if hour.Duration > 0 {
			chart.Empty = false
			minutes = strings.ReplaceAll(fmt.Sprintf("%.1f", hour.Duration.Minutes()), ".", ",")
			if hour.Duration < 6*time.Second {
				minutes = "< 0,1"
			}
		}
		chart.Bars = append(chart.Bars, hourlyBar{Label: label, Range: hour.Start.Format("15:04 MST") + "–" + endLabel, Minutes: minutes, Height: hour.Duration.Minutes() / float64(chart.MaximumMinutes) * 100})
	}
	if len(hours) > 0 {
		chart.Date = hours[0].Start.Format("2006-01-02")
	}
	return chart
}
