package main

import (
	"embed"
	"html/template"
	"time"
)

//go:embed templates/*.html
var templateFiles embed.FS

type dashboard struct {
	Date       string
	Activities []activity
	AdminPath  string
}

var templateFunctions = template.FuncMap{
	"duration": func(seconds int) string {
		return (time.Duration(seconds) * time.Second).String()
	},
	"hours": func(seconds int) int {
		return seconds / 3600
	},
	"minutes": func(seconds int) int {
		return seconds % 3600 / 60
	},
	"quota": func(seconds int) string {
		if seconds == 0 {
			return "Ubegrenset"
		}
		return (time.Duration(seconds) * time.Second).String()
	},
}

var dashboardTemplate = template.Must(template.New("dashboard.html").Funcs(templateFunctions).ParseFS(templateFiles, "templates/dashboard.html"))
var overviewTemplate = template.Must(template.New("overview.html").Funcs(templateFunctions).ParseFS(templateFiles, "templates/overview.html"))
