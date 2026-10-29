//go:build windows

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var lockWorkStation = syscall.NewLazyDLL("user32.dll").NewProc("LockWorkStation")

func clientDataDir() string {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		root, _ = os.UserConfigDir()
	}
	return filepath.Join(root, "ScreenGate")
}

func configureLogging() {
	logDir := clientDataDir()
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return
	}
	logPath := filepath.Join(logDir, "client.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() >= 1<<20 {
		os.Remove(logPath + ".1")
		os.Rename(logPath, logPath+".1")
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		log.SetOutput(file)
	}
}

func main() {
	configureLogging()
	options, err := readClientOptions(flag.CommandLine, os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	if options.debugRemaining != "" {
		runWarningDebug(options.debugRemaining)
		return
	}
	config, err := loadClientConfig(options)
	if err != nil {
		log.Fatal(err)
	}
	guard := enforcement{enabled: options.lockingEnabled(config)}
	log.Printf("test_mode=%t locking_enabled=%t", !guard.enabled, guard.enabled)
	identity := stateIdentity(config.Server, config.DeviceID, config.User)
	mutex, err := acquireClientMutex(identity)
	if err != nil {
		log.Fatal(err)
	}
	defer closeHandle.Call(mutex)
	statePath := filepath.Join(clientDataDir(), "state-"+identity[:16]+".json")
	state, err := loadState(statePath, identity, config.Token, time.Now())
	if err != nil {
		log.Printf("saved authorization unavailable: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	reportTicker := time.NewTicker(30 * time.Second)
	pollTicker := time.NewTicker(time.Second)
	defer reportTicker.Stop()
	defer pollTicker.Stop()
	runClient(ctx, config, state, guard, clientRuntime{
		now:          time.Now,
		pollTicks:    pollTicker.C,
		reportTicks:  reportTicker.C,
		httpClient:   newHTTPClient(),
		save:         func(state clientState) error { return saveState(statePath, config.Token, state) },
		sessionState: currentSessionObservation,
		lock: func() {
			if ok, _, err := lockWorkStation.Call(); ok == 0 {
				log.Printf("lock workstation failed: %v", err)
			}
		},
		showWarning:   showWarning,
		foregroundApp: foregroundApp,
		idleTimeout:   options.idleTimeout,
		trackApps:     options.trackApps,
	})
}

func runWarningDebug(values string) {
	state := warningState{}
	for _, value := range strings.Split(values, ",") {
		remaining, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			log.Printf("invalid debug remaining value: %s", value)
			continue
		}
		warning, _ := state.observe(1, remaining)
		if warning != nil {
			log.Printf("debug warning=%d remaining_seconds=%d", warning.threshold, remaining)
			if err := showWarning(*warning); err != nil {
				log.Printf("warning error: %v", err)
			}
		}
	}
	time.Sleep(6 * time.Second)
}
