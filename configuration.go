package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	_ "time/tzdata"
)

type configuration struct {
	databasePath, adminPath, adminUser, adminPassword, listenAddr string
	location                                                      *time.Location
}

func configuredTrustedOrigins() []string {
	value := os.Getenv("SCREENGATE_TRUSTED_ORIGINS")
	if value == "" {
		return nil
	}
	origins := make([]string, 0, 4)
	for _, item := range strings.Split(value, ",") {
		if origin := strings.TrimSpace(item); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

func loadConfiguration() (configuration, error) {
	get := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	c := configuration{databasePath: get("DATABASE_PATH", "screengate.db"), adminPath: get("ADMIN_PATH", "/admin"), adminUser: get("ADMIN_USER", "admin"), adminPassword: os.Getenv("ADMIN_PASSWORD"), listenAddr: get("LISTEN_ADDR", ":8080")}
	if len(c.adminPassword) < 12 {
		return c, errors.New("set ADMIN_PASSWORD to at least 12 characters before starting ScreenGate")
	}
	if !validIdentity(c.adminUser) {
		return c, errors.New("invalid ADMIN_USER")
	}
	if !validAdminPath(c.adminPath) {
		return c, errors.New("ADMIN_PATH must be a simple path such as /admin and must not conflict with API paths")
	}
	var err error
	c.location, err = time.LoadLocation(get("SCREENGATE_TIMEZONE", "Europe/Oslo"))
	if err != nil {
		return c, fmt.Errorf("invalid SCREENGATE_TIMEZONE: %w", err)
	}
	return c, nil
}

func validAdminPath(path string) bool {
	if len(path) < 2 || len(path) > 128 || path[0] != '/' || strings.Contains(path[1:], "/") {
		return false
	}
	for _, c := range path[1:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	switch path {
	case "/heartbeat", "/event", "/enroll", "/downloads", "/healthz":
		return false
	}
	return true
}
