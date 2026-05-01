// Package config loads nexus-engine configuration from environment variables.
// All settings have sensible defaults for local dev use.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration for nexus-engine.
type Config struct {
	// Mode is "dev" or "prod". Affects network policies and mTLS enforcement.
	Mode string

	// Port is the HTTP API listen port.
	Port string

	// RedisURL is the Redis connection string (e.g. redis://localhost:6379).
	RedisURL string

	// K3sNamespace is the Kubernetes namespace for challenge pods.
	K3sNamespace string

	// Registry holds container registry configuration.
	Registry RegistryConfig

	// NodeAgent holds the node agent gRPC configuration.
	NodeAgent NodeAgentConfig

	// Reconciler holds reconciliation loop configuration.
	Reconciler ReconcilerConfig

	// Session holds session lifecycle configuration.
	Session SessionConfig

	// Challenge holds challenge-specific default settings.
	Challenge ChallengeConfig
}

// ChallengeConfig holds default settings for challenges.
type ChallengeConfig struct {
	// DefaultCPULimit is the fallback CPU limit (e.g. 500m).
	DefaultCPULimit string

	// DefaultMemoryLimit is the fallback memory limit (e.g. 256Mi).
	DefaultMemoryLimit string
}

// RegistryConfig holds container registry settings.
type RegistryConfig struct {
	// URL is the registry base URL (e.g. localhost:5000, ghcr.io).
	URL string

	// AuthType is one of: none, basic, ghcr, awsecr.
	AuthType string

	// Username for basic/ghcr auth.
	Username string

	// Password or token for basic/ghcr auth.
	Password string

	// AWS ECR fields.
	AWSAccount   string
	AWSRegion    string
	AWSAccessKey string
	AWSSecretKey string
}

// NodeAgentConfig holds gRPC connection settings for nexus-node-agent.
type NodeAgentConfig struct {
	// Addr is the gRPC address of the node agent (e.g. localhost:50051).
	Addr string

	// Insecure disables mTLS — only allowed in dev mode.
	Insecure bool

	// TLS certificate paths (used in prod mode).
	TLSCert string
	TLSKey  string
	TLSCA   string
}

// ReconcilerConfig holds reconciliation loop settings.
type ReconcilerConfig struct {
	// Interval is the base reconciliation period (jitter ±20% applied).
	Interval time.Duration

	// MaxWorkers is the size of the worker pool.
	MaxWorkers int

	// RetryBackoff is the initial retry delay on failure.
	RetryBackoff time.Duration
}

// SessionConfig holds session lifecycle settings.
type SessionConfig struct {
	// DefaultTTLMinutes is the default session lifetime.
	DefaultTTLMinutes int

	// MaxPerUser is the maximum concurrent sessions per user_id.
	// 0 means unlimited.
	MaxPerUser int
}

// Load reads configuration from environment variables.
// All values have sensible defaults for local dev mode.
func Load() (*Config, error) {
	mode := getenv("NEXUS_MODE", "dev")
	if mode != "dev" && mode != "prod" {
		return nil, fmt.Errorf("NEXUS_MODE must be 'dev' or 'prod', got %q", mode)
	}

	reconcileInterval, err := parseDuration("NEXUS_RECONCILE_INTERVAL", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("NEXUS_RECONCILE_INTERVAL: %w", err)
	}

	retryBackoff, err := parseDuration("NEXUS_RETRY_BACKOFF", 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("NEXUS_RETRY_BACKOFF: %w", err)
	}

	maxWorkers, err := parseInt("NEXUS_MAX_WORKERS", 5)
	if err != nil {
		return nil, fmt.Errorf("NEXUS_MAX_WORKERS: %w", err)
	}

	defaultTTL, err := parseInt("NEXUS_DEFAULT_SESSION_TTL_MINUTES", 60)
	if err != nil {
		return nil, fmt.Errorf("NEXUS_DEFAULT_SESSION_TTL_MINUTES: %w", err)
	}

	maxPerUser, err := parseInt("NEXUS_MAX_SESSIONS_PER_USER", 0)
	if err != nil {
		return nil, fmt.Errorf("NEXUS_MAX_SESSIONS_PER_USER: %w", err)
	}

	nodeAgentInsecure, err := parseBool("NEXUS_NODE_AGENT_INSECURE", mode == "dev")
	if err != nil {
		return nil, fmt.Errorf("NEXUS_NODE_AGENT_INSECURE: %w", err)
	}

	return &Config{
		Mode:         mode,
		Port:         getenv("NEXUS_PORT", "8081"),
		RedisURL:     getenv("NEXUS_REDIS_URL", "redis://localhost:6379"),
		K3sNamespace: getenv("NEXUS_K3S_NAMESPACE", "nexus-challenges"),
		Registry: RegistryConfig{
			URL:          getenv("NEXUS_REGISTRY_URL", "localhost:5000"),
			AuthType:     getenv("NEXUS_REGISTRY_AUTH_TYPE", "none"),
			Username:     getenv("NEXUS_REGISTRY_AUTH_USERNAME", ""),
			Password:     getenv("NEXUS_REGISTRY_AUTH_PASSWORD", ""),
			AWSAccount:   getenv("NEXUS_REGISTRY_AUTH_AWS_ACCOUNT", ""),
			AWSRegion:    getenv("NEXUS_REGISTRY_AUTH_AWS_REGION", ""),
			AWSAccessKey: getenv("NEXUS_REGISTRY_AUTH_AWS_ACCESS_KEY", ""),
			AWSSecretKey: getenv("NEXUS_REGISTRY_AUTH_AWS_SECRET_KEY", ""),
		},
		NodeAgent: NodeAgentConfig{
			Addr:     getenv("NEXUS_NODE_AGENT_ADDR", "localhost:50051"),
			Insecure: nodeAgentInsecure,
			TLSCert:  getenv("NEXUS_NODE_AGENT_TLS_CERT", "/etc/nexus/agent-client.crt"),
			TLSKey:   getenv("NEXUS_NODE_AGENT_TLS_KEY", "/etc/nexus/agent-client.key"),
			TLSCA:    getenv("NEXUS_NODE_AGENT_TLS_CA", "/etc/nexus/ca.crt"),
		},
		Reconciler: ReconcilerConfig{
			Interval:     reconcileInterval,
			MaxWorkers:   maxWorkers,
			RetryBackoff: retryBackoff,
		},
		Session: SessionConfig{
			DefaultTTLMinutes: defaultTTL,
			MaxPerUser:        maxPerUser,
		},
		Challenge: ChallengeConfig{
			DefaultCPULimit:    getenv("NEXUS_DEFAULT_CPU_LIMIT", "0.5"),
			DefaultMemoryLimit: getenv("NEXUS_DEFAULT_MEMORY_LIMIT", "256Mi"),
		},
	}, nil
}

// IsDev returns true when running in dev mode.
func (c *Config) IsDev() bool { return c.Mode == "dev" }

// IsProd returns true when running in prod mode.
func (c *Config) IsProd() bool { return c.Mode == "prod" }

// ListenAddr returns the full HTTP listen address.
func (c *Config) ListenAddr() string { return ":" + c.Port }

// ─── helpers ─────────────────────────────────────────────────────────────────

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q: %w", raw, err)
	}
	return n, nil
}

func parseBool(key string, fallback bool) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid value %q: %w", raw, err)
	}
	return b, nil
}

func parseDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	// Support plain integers as seconds.
	if n, err := strconv.Atoi(raw); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q: %w", raw, err)
	}
	return d, nil
}
