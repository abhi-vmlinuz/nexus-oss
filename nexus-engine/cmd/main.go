// cmd/main.go is the entrypoint for nexus-engine.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexus-oss/nexus/nexus-engine/internal/api"
	"github.com/nexus-oss/nexus/nexus-engine/internal/config"
	"github.com/nexus-oss/nexus/nexus-engine/internal/controller"
	"github.com/nexus-oss/nexus/nexus-engine/internal/k8s"
	"github.com/nexus-oss/nexus/nexus-engine/internal/nodeagent"
	"github.com/nexus-oss/nexus/nexus-engine/internal/registry"
	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("nexus-engine starting")

	// ── Load configuration ──────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("mode=%s port=%s registry=%s node_agent=%s",
		cfg.Mode, cfg.Port, cfg.Registry.URL, cfg.NodeAgent.Addr)

	// ── Redis ───────────────────────────────────────────────────────────────
	store, err := state.New(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer store.Close()
	log.Printf("Redis connected: %s", cfg.RedisURL)

	// ── Kubernetes ──────────────────────────────────────────────────────────
	k8sClient, err := k8s.New(cfg.K3sNamespace)
	if err != nil {
		log.Fatalf("k8s: %v", err)
	}
	log.Printf("k3s connected: namespace=%s", cfg.K3sNamespace)

	// ── Node Agent ─────────────────────────────────────────────────────────
	var agentClient *nodeagent.Client
	if cfg.NodeAgent.Addr != "" {
		agentClient, err = nodeagent.New(cfg.NodeAgent)
		if err != nil {
			log.Printf("node agent unavailable: %v (continuing without it)", err)
		} else {
			defer agentClient.Close()
			log.Printf("node-agent connected: %s (insecure=%v)", cfg.NodeAgent.Addr, cfg.NodeAgent.Insecure)
		}
	}

	// ── Registry builder ────────────────────────────────────────────────────
	builder := registry.NewBuilder(cfg.Registry)

	// ── Controller ──────────────────────────────────────────────────────────
	ctrl := controller.New(
		store,
		k8sClient,
		agentClient,
		cfg.Reconciler.Interval,
		cfg.Reconciler.MaxWorkers,
		cfg.Reconciler.RetryBackoff,
	)
	ctrl.Start()

	// ── HTTP server ─────────────────────────────────────────────────────────
	if cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(jsonLogger())

	api.Register(r, api.Deps{
		Store:      store,
		K8s:        k8sClient,
		NodeAgent:  agentClient,
		Builder:    builder,
		Controller: ctrl,
		Cfg:        cfg,
	})

	srv := &http.Server{
		Addr:         cfg.ListenAddr(),
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // long for nerdctl builds
		IdleTimeout:  60 * time.Second,
	}

	// Start server in background.
	go func() {
		log.Printf("listening on %s", cfg.ListenAddr())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	// ── Graceful shutdown ────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Printf("shutting down nexus-engine…")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	log.Printf("nexus-engine stopped")
}

// jsonLogger is a minimal structured request logger.
func jsonLogger() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(p gin.LogFormatterParams) string {
		if p.StatusCode >= 500 {
			return fmt.Sprintf(`{"ts":%q,"method":%q,"path":%q,"status":%d,"latency_ms":%d,"error":%q}`+"\n",
				p.TimeStamp.UTC().Format(time.RFC3339),
				p.Method, p.Path, p.StatusCode,
				p.Latency.Milliseconds(), p.ErrorMessage)
		}
		return fmt.Sprintf(`{"ts":%q,"method":%q,"path":%q,"status":%d,"latency_ms":%d}`+"\n",
			p.TimeStamp.UTC().Format(time.RFC3339),
			p.Method, p.Path, p.StatusCode, p.Latency.Milliseconds())
	})
}
