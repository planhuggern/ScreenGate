//go:build windows

package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"os/user"
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
	configPath := flag.String("config", "", "path to protected installation configuration")
	testMode := flag.Bool("test-mode", false, "force safe test mode: never lock Windows")
	enableLocking := flag.Bool("enable-locking", false, "explicitly enable Windows locking")
	endpoint := flag.String("server", "", "ScreenGate heartbeat URL (http(s)://host:port/heartbeat)")
	token := flag.String("token", "", "device token; prefer -token-file or protected -config")
	tokenPath := flag.String("token-file", "", "file containing the device token")
	userFlag := flag.String("user", "", "logical ScreenGate user assigned during pairing")
	deviceFlag := flag.String("device-id", "", "device ID assigned during pairing")
	idleTimeout := flag.Duration("idle-timeout", 0, "optional inactivity cutoff for app tracking, e.g. 5m; unlocked time still counts")
	trackApps := flag.Bool("track-apps", false, "optional foreground application reporting (disabled by default)")
	debugRemaining := flag.String("debug-remaining", "", "comma-separated remaining_seconds values for warning testing")
	flag.Parse()
	if *debugRemaining != "" {
		runWarningDebug(*debugRemaining)
		return
	}
	if *idleTimeout < 0 {
		log.Fatal("idle-timeout cannot be negative")
	}
	config := clientConfig{Token: strings.TrimSpace(os.Getenv("SCREENGATE_DEVICE_TOKEN"))}
	if *configPath != "" {
		var err error
		config, err = readConfig(*configPath)
		if err != nil {
			log.Fatal(err)
		}
	}
	if *endpoint != "" {
		config.Server = *endpoint
	}
	if *token != "" {
		config.Token = strings.TrimSpace(*token)
	}
	if *tokenPath != "" {
		data, err := os.ReadFile(*tokenPath)
		if err != nil {
			log.Fatal("cannot read device token: ", err)
		}
		config.Token = strings.TrimSpace(string(data))
	}
	if *userFlag != "" {
		config.User = *userFlag
	}
	if *deviceFlag != "" {
		config.DeviceID = *deviceFlag
	}
	if config.DeviceID == "" {
		var err error
		config.DeviceID, err = os.Hostname()
		if err != nil {
			log.Fatal(err)
		}
	}
	if config.User == "" {
		currentUser, err := user.Current()
		if err != nil {
			log.Fatal(err)
		}
		config.User = currentUser.Username
	}
	var err error
	config.Server, err = validateEndpoint(config.Server)
	if err != nil {
		log.Fatal(err)
	}
	if config.Token == "" {
		log.Fatal("device token missing; pair this Windows user using install.ps1")
	}
	guard := enforcement{enabled: (config.EnableLocking || *enableLocking) && !*testMode}
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
		sessionState: currentSessionState,
		lock: func() {
			if ok, _, err := lockWorkStation.Call(); ok == 0 {
				log.Printf("lock workstation failed: %v", err)
			}
		},
		showWarning:   showWarning,
		foregroundApp: foregroundApp,
		idleTimeout:   *idleTimeout,
		trackApps:     *trackApps,
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
