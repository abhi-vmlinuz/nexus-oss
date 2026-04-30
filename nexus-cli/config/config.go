// Package config handles nexus-cli configuration (~/.config/nexus/config.json).
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/redis/go-redis/v9"
)

type Config struct {
	Engine struct {
		URL  string `json:"url"`
		Mode string `json:"mode"`
	} `json:"engine"`
	Registry struct {
		Type string `json:"type"`
		URL  string `json:"url"`
		Auth struct {
			Type     string `json:"type"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"auth"`
	} `json:"registry"`
	Redis struct {
		URL string `json:"url"`
	} `json:"redis"`
	NodeAgent struct {
		Addr string `json:"addr"`
	} `json:"node_agent"`
	K8s struct {
		Namespace string `json:"namespace"`
	} `json:"k8s"`
}

type ConfigNotFoundError struct {
	Path    string
	Message string
}

func (e *ConfigNotFoundError) Error() string {
	return e.Message
}

func configPath() string {
	return os.ExpandEnv("$HOME/.config/nexus/config.json")
}

// Path returns the config file path for display purposes.
func Path() string {
	return configPath()
}

// LoadConfig reads config from ~/.config/nexus/config.json
func LoadConfig() (*Config, error) {
	path := configPath()

	data, err := ioutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ConfigNotFoundError{
				Path: path,
				Message: fmt.Sprintf(
					"config file not found: %s\n\n"+
						"To bootstrap Nexus:\n"+
						"  sudo bash deploy/setup.sh\n\n"+
						"To create config manually:\n"+
						"  nexus config init\n\n"+
						"For more info:\n"+
						"  nexus config --help",
					path,
				),
			}
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config JSON at %s: %w", path, err)
	}

	// Validate required fields
	if cfg.Engine.URL == "" {
		return nil, fmt.Errorf("config error: engine.url is required")
	}

	return &cfg, nil
}

// LoadConfigWithEnvFallback falls back to env vars if config doesn't exist (but warn user)
func LoadConfigWithEnvFallback() (*Config, error) {
	cfg, err := LoadConfig()

	// If config file missing, try env vars
	if err != nil {
		if _, ok := err.(*ConfigNotFoundError); ok {
			return loadConfigFromEnv()
		}
		return nil, err
	}

	return cfg, nil
}

func loadConfigFromEnv() (*Config, error) {
	cfg := &Config{}

	cfg.Engine.URL = os.Getenv("NEXUS_ENGINE_URL")
	cfg.Engine.Mode = os.Getenv("NEXUS_MODE")
	cfg.Registry.URL = os.Getenv("NEXUS_REGISTRY_URL")
	cfg.Redis.URL = os.Getenv("NEXUS_REDIS_URL")
	cfg.NodeAgent.Addr = os.Getenv("NEXUS_NODE_AGENT_ADDR")
	cfg.K8s.Namespace = os.Getenv("NEXUS_K3S_NAMESPACE")

	if cfg.Engine.URL == "" {
		return nil, fmt.Errorf(
			"config not found and NEXUS_ENGINE_URL env var not set\n" +
				"Run: sudo bash deploy/setup.sh",
		)
	}

	// Warn that env vars are being used
	fmt.Fprintf(os.Stderr,
		"WARNING: Using environment variables for config (not recommended)\n"+
			"To create config file: nexus config init\n\n",
	)

	return cfg, nil
}

// CheckEnvMismatch checks for config mismatch (systemd env vars vs config file)
func (c *Config) CheckEnvMismatch() {
	envURL := os.Getenv("NEXUS_ENGINE_URL")
	if envURL != "" && envURL != c.Engine.URL {
		fmt.Fprintf(os.Stderr,
			"WARNING: systemd env var NEXUS_ENGINE_URL does not match config file\n"+
				"  env var:   %s\n"+
				"  config:    %s\n"+
				"Using config file value. To sync:\n"+
				"  sudo systemctl set-environment NEXUS_ENGINE_URL=%s\n\n",
			envURL, c.Engine.URL, c.Engine.URL,
		)
	}
}

// Display prints the config in a human-readable format.
func (c *Config) Display() {
	fmt.Printf("Nexus Configuration\n")
	fmt.Printf("Location: %s\n\n", configPath())
	fmt.Printf("Engine:\n")
	fmt.Printf("  url: %s\n", c.Engine.URL)
	fmt.Printf("  mode: %s\n\n", c.Engine.Mode)

	fmt.Printf("Registry:\n")
	fmt.Printf("  type: %s\n", c.Registry.Type)
	fmt.Printf("  url: %s\n", c.Registry.URL)
	fmt.Printf("  auth.type: %s\n\n", c.Registry.Auth.Type)

	fmt.Printf("Redis:\n")
	fmt.Printf("  url: %s\n\n", c.Redis.URL)

	fmt.Printf("Node Agent:\n")
	fmt.Printf("  addr: %s\n\n", c.NodeAgent.Addr)

	fmt.Printf("K8s:\n")
	fmt.Printf("  namespace: %s\n", c.K8s.Namespace)
}

// Set updates a configuration key and saves it.
func (c *Config) Set(key, value string) error {
	parts := strings.Split(key, ".")

	if len(parts) < 2 {
		return fmt.Errorf("invalid key format, expected category.field")
	}

	switch parts[0] {
	case "engine":
		if parts[1] == "url" {
			c.Engine.URL = value
		} else if parts[1] == "mode" {
			c.Engine.Mode = value
		} else {
			return fmt.Errorf("unknown key: %s", key)
		}
	case "registry":
		if parts[1] == "type" {
			c.Registry.Type = value
		} else if parts[1] == "url" {
			c.Registry.URL = value
		} else if parts[1] == "auth" {
			if len(parts) == 3 {
				if parts[2] == "type" {
					c.Registry.Auth.Type = value
				} else if parts[2] == "username" {
					c.Registry.Auth.Username = value
				} else if parts[2] == "password" {
					c.Registry.Auth.Password = value
				} else {
					return fmt.Errorf("unknown key: %s", key)
				}
			} else {
				return fmt.Errorf("unknown key: %s", key)
			}
		} else {
			return fmt.Errorf("unknown key: %s", key)
		}
	case "redis":
		if parts[1] == "url" {
			c.Redis.URL = value
		} else {
			return fmt.Errorf("unknown key: %s", key)
		}
	case "node_agent":
		if parts[1] == "addr" {
			c.NodeAgent.Addr = value
		} else {
			return fmt.Errorf("unknown key: %s", key)
		}
	case "k8s":
		if parts[1] == "namespace" {
			c.K8s.Namespace = value
		} else {
			return fmt.Errorf("unknown key: %s", key)
		}
	default:
		return fmt.Errorf("unknown category: %s", parts[0])
	}

	return c.Save()
}

// Save writes the config back to disk.
func (c *Config) Save() error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return ioutil.WriteFile(path, data, 0644)
}

// Validate checks if the configured services are responding.
func (c *Config) Validate() error {
	var errors []string

	// Check engine
	if c.Engine.URL != "" {
		resp, err := http.Get(c.Engine.URL + "/health")
		if err != nil || resp.StatusCode != 200 {
			errors = append(errors, fmt.Sprintf(
				"✗ engine.url not responding (%s/health)\n"+
					"  The Engine may not be running\n"+
					"  Check: systemctl status nexus-engine",
				c.Engine.URL))
		} else {
			fmt.Println("✓ engine.url is valid (responds to health check)")
		}
	} else {
		errors = append(errors, "✗ engine.url is empty")
	}

	// Check redis
	if c.Redis.URL != "" {
		client := redis.NewClient(&redis.Options{Addr: strings.TrimPrefix(c.Redis.URL, "redis://")}) // Basic check
		if _, err := client.Ping(context.Background()).Result(); err != nil {
			errors = append(errors, fmt.Sprintf(
				"✗ redis.url not responding (%s)\n"+
					"  Check: systemctl status nexus-redis (or docker ps | grep redis)",
				c.Redis.URL))
		} else {
			fmt.Println("✓ redis.url is responding")
		}
	} else {
		errors = append(errors, "✗ redis.url is empty")
	}

	if len(errors) > 0 {
		return fmt.Errorf(strings.Join(errors, "\n\n"))
	}

	return nil
}
