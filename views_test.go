package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestDashboardEscapesUserControlledContent(t *testing.T) {
	malicious := `<script>alert("name")</script>`
	model := dashboard{Date: "2026-09-18", AdminPath: "/admin", CSRFToken: "csrf", Activities: []activity{{User: malicious, QuotaSeconds: 3600, RemainingSeconds: 3600}}, Devices: []deviceView{{ID: "id", DeviceID: malicious, User: malicious}}, PairingCode: "SAFE-CODE", PairingUser: malicious, Audit: []auditEvent{{User: malicious, Description: malicious}}}
	var page bytes.Buffer
	if err := dashboardTemplate.Execute(&page, model); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page.String(), malicious) || !strings.Contains(page.String(), "&lt;script&gt;") {
		t.Fatal("dashboard did not escape user content")
	}
	if strings.Count(page.String(), `name="csrf_token"`) < 5 {
		t.Fatal("missing form tokens")
	}
}

func TestDashboardSummarizesWeekWithoutInventedActivity(t *testing.T) {
	model := dashboard{Date: "2026-09-18", Activities: []activity{{User: "A", Online: true, TotalSeconds: 60, QuotaSeconds: 600, History: []dailyUsage{{Date: "2026-09-17", TotalSeconds: 120}, {Date: "2026-09-18", TotalSeconds: 60}}}, {User: "B", Paused: true, Unlimited: true, TotalSeconds: 30}}}
	summary := model.Summary()
	if summary.TotalSeconds != 90 || summary.WeekSeconds != 210 || summary.Online != 1 || summary.Paused != 1 || summary.Limited != 1 || len(summary.Days) != 7 {
		t.Fatalf("summary=%+v", summary)
	}
	if summary.Days[0].Bars[0].Height != 0 || summary.Days[5].Bars[0].TotalSeconds != 120 || !summary.Days[6].Today {
		t.Fatalf("bars=%+v", summary.Days)
	}
	for _, day := range summary.Days {
		if len(day.Bars) != 2 || day.Bars[0].User != "A" || day.Bars[1].User != "B" || day.Bars[0].Color != summary.Legend[0].Color {
			t.Fatalf("inconsistent series: %+v", day)
		}
	}
	if summary.Days[5].Bars[1].TotalSeconds != 0 || summary.Days[6].Bars[0].TotalSeconds != 60 || summary.Days[6].Bars[1].TotalSeconds != 30 || summary.Days[6].Bars[0].Height != 2*summary.Days[6].Bars[1].Height {
		t.Fatalf("incorrect per-user totals or scale: %+v", summary.Days)
	}
	var page bytes.Buffer
	if err := dashboardTemplate.Execute(&page, model); err != nil {
		t.Fatal(err)
	}
	if strings.Count(page.String(), `class="weekly-user-bar"`) != 14 || strings.Contains(page.String(), "ZgotmplZ") {
		t.Fatal("grouped weekly chart missing or invalid")
	}
}

func TestQuotaDisplayAndStatus(t *testing.T) {
	if humanDuration(3660) != "1 t 1 min" || humanDuration(30) != "under 1 min" || clockMinute(1440) != "24:00" {
		t.Fatal("time formatting regression")
	}
	if initials("Nora Åsen") != "NÅ" {
		t.Fatal("Norwegian initials broken")
	}
	label, _, detail := policyStatus(activity{Reason: "schedule", NextAllowedAt: time.Date(2026, 9, 18, 15, 0, 0, 0, time.UTC)})
	if label != "Skjermfri tid" || !strings.Contains(detail, "15:00") {
		t.Fatalf("status=%s %s", label, detail)
	}
}
