package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"time"
)

// Startup options are separate from the protected installation configuration.
// Their names and defaults are the existing command-line interface.
type clientOptions struct {
	configPath     string
	server         string
	user           string
	deviceID       string
	token          string
	tokenPath      string
	testMode       bool
	enableLocking  bool
	idleTimeout    time.Duration
	trackApps      bool
	debugRemaining string
}

func readClientOptions(flags *flag.FlagSet, args []string) (clientOptions, error) {
	var options clientOptions
	flags.StringVar(&options.configPath, "config", "", "path to protected installation configuration")
	flags.BoolVar(&options.testMode, "test-mode", false, "force safe test mode: never lock Windows")
	flags.BoolVar(&options.enableLocking, "enable-locking", false, "explicitly enable Windows locking")
	flags.StringVar(&options.server, "server", "", "ScreenGate heartbeat URL (http(s)://host:port/heartbeat)")
	flags.StringVar(&options.token, "token", "", "device token; prefer -token-file or protected -config")
	flags.StringVar(&options.tokenPath, "token-file", "", "file containing the device token")
	flags.StringVar(&options.user, "user", "", "logical ScreenGate user assigned during pairing")
	flags.StringVar(&options.deviceID, "device-id", "", "device ID assigned during pairing")
	flags.DurationVar(&options.idleTimeout, "idle-timeout", 0, "optional inactivity cutoff for app tracking, e.g. 5m; unlocked time still counts")
	flags.BoolVar(&options.trackApps, "track-apps", false, "optional foreground application reporting (disabled by default)")
	flags.StringVar(&options.debugRemaining, "debug-remaining", "", "comma-separated remaining_seconds values for warning testing")
	err := flags.Parse(args)
	return options, err
}

func (o clientOptions) lockingEnabled(config clientConfig) bool {
	return (config.EnableLocking || o.enableLocking) && !o.testMode
}

type clientConfig struct {
	EnableLocking bool   `json:"enable_locking"`
	Server        string `json:"server"`
	Token         string `json:"token"`
	DeviceID      string `json:"device_id"`
	User          string `json:"user"`
}

// Resolve settings in the existing order: environment, configuration file,
// command-line overrides, then identity defaults. A token file has precedence
// over the token argument. A configuration file replaces the environment token.
func loadClientConfig(options clientOptions) (clientConfig, error) {
	if options.idleTimeout < 0 {
		return clientConfig{}, errors.New("idle-timeout cannot be negative")
	}
	config := clientConfig{Token: strings.TrimSpace(os.Getenv("SCREENGATE_DEVICE_TOKEN"))}
	if options.configPath != "" {
		var err error
		config, err = readConfig(options.configPath)
		if err != nil {
			return clientConfig{}, err
		}
	}
	if options.server != "" {
		config.Server = options.server
	}
	if options.token != "" {
		config.Token = strings.TrimSpace(options.token)
	}
	if options.tokenPath != "" {
		data, err := os.ReadFile(options.tokenPath)
		if err != nil {
			return clientConfig{}, fmt.Errorf("cannot read device token: %w", err)
		}
		config.Token = strings.TrimSpace(string(data))
	}
	if options.user != "" {
		config.User = options.user
	}
	if options.deviceID != "" {
		config.DeviceID = options.deviceID
	}
	if config.DeviceID == "" {
		var err error
		config.DeviceID, err = os.Hostname()
		if err != nil {
			return clientConfig{}, err
		}
	}
	if config.User == "" {
		currentUser, err := user.Current()
		if err != nil {
			return clientConfig{}, err
		}
		config.User = currentUser.Username
	}
	var err error
	config.Server, err = validateEndpoint(config.Server)
	if err != nil {
		return clientConfig{}, err
	}
	if config.Token == "" {
		return clientConfig{}, errors.New("device token missing; pair this Windows user using install.ps1")
	}
	return config, nil
}

func readConfig(path string) (clientConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return clientConfig{}, err
	}
	defer file.Close()
	var config clientConfig
	decoder := json.NewDecoder(io.LimitReader(file, 64*1024))
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("invalid client configuration: %w", err)
	}
	config.Token = strings.TrimSpace(config.Token)
	return config, nil
}
