// Package config handles nexus-cli configuration (~/.config/nexus/config.json).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	configDir  = ".config/nexus"
	configFile = "config.json"
)

// CLIConfig holds nexus-cli runtime configuration.
type CLIConfig struct {
	// EngineURL is the base URL of nexus-engine (e.g. http://localhost:8081).
	EngineURL string `json:"engine_url"`
	// OutputFormat is "table" or "json".
	OutputFormat string `json:"output_format"`
}

// DefaultConfig returns default configuration values.
func Default() *CLIConfig {
	return &CLIConfig{
		EngineURL:    "http://localhost:8081",
		OutputFormat: "table",
	}
}

// configPath returns the full path to the config file.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home dir: %w", err)
	}
	return filepath.Join(home, configDir, configFile), nil
}

// Load reads config from disk, returning defaults if the file doesn't exist.
func Load() (*CLIConfig, error) {
	path, err := configPath()
	if err != nil {
		return Default(), nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

// Save writes config to disk, creating parent directories as needed.
func (c *CLIConfig) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Path returns the config file path for display purposes.
func Path() string {
	path, _ := configPath()
	return path
}
