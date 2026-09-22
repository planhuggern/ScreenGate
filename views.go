package main

import (
	"embed"
	"fmt"
	"html/template"
	"strings"
	"time"
	"unicode"
)

//go:embed templates/*.html
var templateFiles embed.FS

type dashboard struct {
	Date         string
	Activities   []activity
	AdminPath    string
	CSRFToken    string
	Timezone     string
	Flash        string
	PairingCode  string
	PairingUser  string
	ClientSHA256 string
	Devices      []deviceView
	Audit        []auditEvent
}

type dashboardSummary struct {
	TotalSeconds   int
	Online         int
	Paused         int
	Limited        int
	WeekSeconds    int
	DailyAverage   int
	Days           []historyBar
	Legend         []weeklySeries
	MaximumSeconds int
}

type weeklySeries struct {
	User  string
	Color int
}

type weeklyUserBar struct {
	weeklySeries
	TotalSeconds int
	Height       float64
}

type historyBar struct {
	Date         string
	Label        string
	TotalSeconds int
	Bars         []weeklyUserBar
	Today        bool
}

type userCard struct {
	activity
	Index        int
	Initials     string
	Status       string
	StatusTone   string
	StatusDetail string
	UsagePercent int
	LastSeen     string
	RuleDays     []ruleDay
	HourlyChart  hourlyChart
}

type ruleDay struct {
	weekdayPolicy
	Name string
}

func (d dashboard) Summary() dashboardSummary {
	var result dashboardSummary
	dailyTotals := make(map[string]int)
	userTotals := make([]map[string]int, len(d.Activities))
	for index, item := range d.Activities {
		// Golden-angle hues keep each user's color consistent across the week.
		result.Legend = append(result.Legend, weeklySeries{User: item.User, Color: (index*137 + 140) % 360})
		userTotals[index] = make(map[string]int)
		result.TotalSeconds += item.TotalSeconds
		if item.Online {
			result.Online++
		}
		if item.Paused {
			result.Paused++
		}
		if !item.Unlimited && item.QuotaSeconds > 0 {
			result.Limited++
		}
		for _, day := range item.History {
			dailyTotals[day.Date] += day.TotalSeconds
			userTotals[index][day.Date] += day.TotalSeconds
		}
		userTotals[index][d.Date] = item.TotalSeconds
	}
	end, err := time.Parse("2006-01-02", d.Date)
	if err != nil {
		end = time.Now()
	}
	result.MaximumSeconds = 3600
	for offset := -6; offset <= 0; offset++ {
		date := end.AddDate(0, 0, offset)
		key := date.Format("2006-01-02")
		total := dailyTotals[key]
		if offset == 0 {
			total = result.TotalSeconds
		}
		result.WeekSeconds += total
		group := historyBar{Date: key, Label: shortWeekdays[date.Weekday()], TotalSeconds: total, Today: offset == 0}
		for index, series := range result.Legend {
			seconds := userTotals[index][key]
			result.MaximumSeconds = max(result.MaximumSeconds, ((seconds+1799)/1800)*1800)
			group.Bars = append(group.Bars, weeklyUserBar{weeklySeries: series, TotalSeconds: seconds})
		}
		result.Days = append(result.Days, group)
	}
	for i := range result.Days {
		for j := range result.Days[i].Bars {
			bar := &result.Days[i].Bars[j]
			bar.Height = float64(bar.TotalSeconds) / float64(result.MaximumSeconds) * 100
		}
	}
	result.DailyAverage = result.WeekSeconds / 7
	return result
}

func (d dashboard) Users() []userCard {
	users := make([]userCard, 0, len(d.Activities))
	for index, item := range d.Activities {
		card := userCard{activity: item, Index: index, Initials: initials(item.User), LastSeen: lastSeen(item.LastReportedAt)}
		card.Status, card.StatusTone, card.StatusDetail = policyStatus(item)
		card.HourlyChart = buildHourlyChart(item.HourlyUsage)
		if item.QuotaSeconds > 0 {
			card.UsagePercent = min(100, max(0, int(float64(item.TotalSeconds)/float64(item.QuotaSeconds)*100)))
		}
		byDay := make(map[int]weekdayPolicy)
		for _, day := range item.Weekdays {
			byDay[day.Weekday] = day
		}
		for _, day := range []int{1, 2, 3, 4, 5, 6, 0} {
			policy, exists := byDay[day]
			if !exists {
				policy = weekdayPolicy{Weekday: day, QuotaSeconds: item.BaseQuotaSeconds, EndMinute: 1440}
			}
			card.RuleDays = append(card.RuleDays, ruleDay{weekdayPolicy: policy, Name: longWeekdays[day]})
		}
		users = append(users, card)
	}
	return users
}

func policyStatus(item activity) (label, tone, detail string) {
	switch {
	case item.Paused || item.Reason == "paused":
		return "Satt på pause", "amber", "Skjermtilgangen er satt på pause av en foresatt."
	case item.Reason == "schedule":
		detail = "Utenfor tillatt skjermtid."
		if !item.NextAllowedAt.IsZero() {
			detail += " Neste åpning: " + shortWeekdays[item.NextAllowedAt.Weekday()] + ". " + item.NextAllowedAt.Format("15:04") + "."
		}
		return "Skjermfri tid", "slate", detail
	case item.Reason == "quota" || item.Action == "lock":
		return "Dagens tid er brukt", "amber", "Dagens tidsgrense er nådd. Du kan gi litt ekstra tid nedenfor."
	case item.Unlimited || item.QuotaSeconds == 0:
		return "Uten tidsgrense", "slate", "Ingen daglig tidsgrense. Tidsplan og pause gjelder fortsatt."
	default:
		return "Tid igjen", "green", "Innenfor dagens tidsgrense og tidsplan."
	}
}

var shortWeekdays = [...]string{"søn", "man", "tir", "ons", "tor", "fre", "lør"}
var longWeekdays = [...]string{"Søndag", "Mandag", "Tirsdag", "Onsdag", "Torsdag", "Fredag", "Lørdag"}
var norwegianMonths = [...]string{"", "januar", "februar", "mars", "april", "mai", "juni", "juli", "august", "september", "oktober", "november", "desember"}

func humanDuration(seconds int) string {
	if seconds <= 0 {
		return "0 min"
	}
	if seconds < 60 {
		return "under 1 min"
	}
	hours, minutes := seconds/3600, seconds%3600/60
	if hours == 0 {
		return fmt.Sprintf("%d min", minutes)
	}
	if minutes == 0 {
		return fmt.Sprintf("%d t", hours)
	}
	return fmt.Sprintf("%d t %d min", hours, minutes)
}

func initials(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(`\/@._-`, r) })
	if len(parts) == 0 {
		return "?"
	}
	if len(parts) == 1 {
		return strings.ToUpper(string([]rune(parts[0])[0]))
	}
	return strings.ToUpper(string([]rune(parts[0])[0]) + string([]rune(parts[len(parts)-1])[0]))
}

func lastSeen(raw string) string {
	if raw == "" {
		return "Venter på første rapport"
	}
	timestamp, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	age := time.Since(timestamp)
	switch {
	case age < 2*time.Minute:
		return "Rapportert akkurat nå"
	case age < time.Hour:
		return fmt.Sprintf("Rapportert for %d min siden", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("Rapportert for %d t siden", int(age.Hours()))
	default:
		return "Sist rapportert " + timestamp.Format("02.01.2006 15:04")
	}
}

func clockMinute(value int) string {
	value = min(1440, max(0, value))
	return fmt.Sprintf("%02d:%02d", value/60, value%60)
}

var templateFunctions = template.FuncMap{
	"duration": func(seconds int) string {
		return (time.Duration(seconds) * time.Second).String()
	},
	"hours": func(seconds int) int {
		return max(0, seconds) / 3600
	},
	"minutes": func(seconds int) int {
		return max(0, seconds) % 3600 / 60
	},
	"quota": func(seconds int) string {
		if seconds <= 0 {
			return "Ubegrenset"
		}
		return humanDuration(seconds)
	},
	"humanDuration": humanDuration,
	"clockMinute":   clockMinute,
	"lastSeen":      lastSeen,
	"norwegianDate": func(raw string) string {
		date, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return raw
		}
		return fmt.Sprintf("%s %d. %s", longWeekdays[date.Weekday()], date.Day(), norwegianMonths[date.Month()])
	},
}

var dashboardTemplate = template.Must(template.New("dashboard.html").Funcs(templateFunctions).ParseFS(templateFiles, "templates/dashboard.html", "templates/styles.html", "templates/hourly_chart.html"))
var overviewTemplate = template.Must(template.New("overview.html").Funcs(templateFunctions).ParseFS(templateFiles, "templates/overview.html", "templates/styles.html"))
