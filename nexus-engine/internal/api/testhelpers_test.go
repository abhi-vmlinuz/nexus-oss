package api_test

import (
	"testing"

	"github.com/nexus-oss/nexus/nexus-engine/internal/api"
	"github.com/nexus-oss/nexus/nexus-engine/internal/config"
	"github.com/nexus-oss/nexus/nexus-engine/internal/controller"
	"github.com/nexus-oss/nexus/nexus-engine/internal/k8s"
	"github.com/nexus-oss/nexus/nexus-engine/internal/registry"
	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
)

// minimalDeps returns api.Deps backed by a real Redis (skipped if unavailable)
// and mock k8s/registry that don't make real calls.
func minimalDeps(t *testing.T) api.Deps {
	t.Helper()

	// Try real Redis for state.
	var store *state.Store
	s, err := state.New("redis://localhost:6379")
	if err != nil {
		t.Skipf("Redis unavailable — skipping API test: %v", err)
	}
	store = s
	t.Cleanup(func() { store.Close() })

	cfg := &config.Config{
		Mode: "dev",
		Port: "8081",
		Session: config.SessionConfig{
			DefaultTTLMinutes: 60,
			MaxPerUser:        0,
		},
		Registry:   config.RegistryConfig{URL: "localhost:5000"},
		NodeAgent:  config.NodeAgentConfig{Insecure: true},
		Reconciler: config.ReconcilerConfig{MaxWorkers: 1},
	}

	// nil k8s and agent — handlers referencing them will only be exercised by
	// integration tests that provide real k8s access.
	ctrl := controller.New(store, (*k8s.Client)(nil), nil,
		0, 1, 0)

	return api.Deps{
		Store:      store,
		K8s:        nil,
		NodeAgent:  nil,
		Builder:    registry.NewBuilder(cfg.Registry),
		Controller: ctrl,
		Cfg:        cfg,
	}
}
