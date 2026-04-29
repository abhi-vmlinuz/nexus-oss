// Package api registers all HTTP routes for nexus-engine.
package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexus-oss/nexus/nexus-engine/internal/config"
	"github.com/nexus-oss/nexus/nexus-engine/internal/controller"
	"github.com/nexus-oss/nexus/nexus-engine/internal/k8s"
	"github.com/nexus-oss/nexus/nexus-engine/internal/nodeagent"
	"github.com/nexus-oss/nexus/nexus-engine/internal/registry"
	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
)

// Deps bundles all handler dependencies.
type Deps struct {
	Store      *state.Store
	K8s        *k8s.Client
	NodeAgent  *nodeagent.Client // may be nil in dev mode
	Builder    *registry.Builder
	Controller *controller.Controller
	Cfg        *config.Config
}

// Register wires all HTTP routes onto the gin engine.
func Register(r *gin.Engine, d Deps) {
	// Health
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"service":   "nexus-engine",
			"mode":      d.Cfg.Mode,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	// Metrics (prometheus)
	r.GET("/metrics", metricsHandler())

	// Debug endpoints
	dbg := r.Group("/debug")
	{
		dbg.GET("/system", newDebugHandler(d).System)
		dbg.GET("/controller", newDebugHandler(d).Controller)
	}

	v1 := r.Group("/api/v1")
	{
		// Challenge management
		ch := newChallengeHandler(d)
		v1.POST("/challenges", ch.Create)
		v1.GET("/challenges", ch.List)
		v1.GET("/challenges/:id", ch.Get)
		v1.DELETE("/challenges/:id", ch.Delete)
		v1.POST("/challenges/:id/rebuild", ch.Rebuild)

		// Session management
		sh := newSessionHandler(d)
		v1.POST("/sessions", sh.Create)
		v1.GET("/sessions", sh.List)
		v1.GET("/sessions/:id", sh.Get)
		v1.DELETE("/sessions/:id", sh.Terminate)
		v1.POST("/sessions/:id/extend", sh.Extend)

		// Admin / operator endpoints
		adm := v1.Group("/admin")
		ah := newAdminHandler(d)
		adm.GET("/sessions", ah.Sessions)
		adm.GET("/nodes", ah.Nodes)
		adm.GET("/cluster/health", ah.ClusterHealth)
		adm.GET("/config", ah.Config)
		adm.POST("/reconcile", ah.TriggerReconcile)
	}
}
