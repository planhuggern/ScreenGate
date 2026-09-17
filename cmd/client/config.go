package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type clientConfig struct {
	Server   string `json:"server"`
	Token    string `json:"token"`
	DeviceID string `json:"device_id"`
	User     string `json:"user"`
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
