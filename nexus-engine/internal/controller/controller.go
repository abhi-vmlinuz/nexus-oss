// Package controller implements the Nexus reconciliation loop.
// It runs as a background goroutine inside nexus-engine, continuously converging
// the desired state (stored in Redis) with the actual state (k3s + node agent).
//
// Design mirrors the production ZecurX controller:
//   - Worker pool (configurable size) drains a job channel
//   - Periodic scan enqueues all active sessions
//   - Touch() enqueues on mutation (create/terminate/extend)
//   - Idempotent: duplicate enqueues for in-flight sessions are dropped
//   - Retry with backoff on failure
package controller

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/nexus-oss/nexus/nexus-engine/internal/k8s"
	"github.com/nexus-oss/nexus/nexus-engine/internal/nodeagent"
	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
)

// ─── Metrics ──────────────────────────────────────────────────────────────────

var (
	metricCyclesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "nexus_reconcile_cycles_total",
		Help: "Total number of reconcile cycles executed.",
	})
	metricRepairsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "nexus_reconcile_repairs_total",
		Help: "Total number of repairs performed by the reconciler.",
	})
	metricRPCErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "nexus_nodeagent_rpc_errors_total",
		Help: "Total node agent RPC errors encountered during reconciliation.",
	})
)

// ─── Controller ───────────────────────────────────────────────────────────────

const (
	defaultInterval  = 15 * time.Second
	defaultWorkers   = 5
	defaultBackoff   = 5 * time.Second
	maxBackoff       = 2 * time.Minute
)

// reconcileJob is a unit of work for the worker pool.
type reconcileJob struct {
	sessionID string
	reason    string
}

// Stats holds runtime statistics for the debug endpoint.
type Stats struct {
	Queued   int    `json:"queued"`
	InFlight int    `json:"in_flight"`
	Interval string `json:"reconcile_interval"`
	Workers  int    `json:"workers"`
	Status   string `json:"status"`
}

// Controller manages reconciliation for session desired/actual state convergence.
type Controller struct {
	store     *state.Store
	k8s       *k8s.Client
	agent     *nodeagent.Client // may be nil in dev mode
	interval  time.Duration
	workers   int
	backoff   time.Duration

	jobs     chan reconcileJob
	mu       sync.Mutex
	queued   map[string]bool
	inFlight map[string]bool
	started  bool
}

// New creates a Controller with the given dependencies and configuration.
func New(
	store *state.Store,
	k8sClient *k8s.Client,
	agentClient *nodeagent.Client,
	interval time.Duration,
	workers int,
	backoff time.Duration,
) *Controller {
	if interval <= 0 {
		interval = defaultInterval
	}
	if workers <= 0 {
		workers = defaultWorkers
	}
	if backoff <= 0 {
		backoff = defaultBackoff
	}
	return &Controller{
		store:    store,
		k8s:      k8sClient,
		agent:    agentClient,
		interval: interval,
		workers:  workers,
		backoff:  backoff,
		jobs:     make(chan reconcileJob, workers*64),
		queued:   make(map[string]bool),
		inFlight: make(map[string]bool),
	}
}

// Start launches the worker pool and background loops. Safe to call once.
func (c *Controller) Start() {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.mu.Unlock()

	for i := 0; i < c.workers; i++ {
		go c.workerLoop(i)
	}
	go c.bootstrapLoop()
	go c.periodicLoop()
	go c.cleanupLoop()

	log.Printf("🔁 Controller started | interval=%s workers=%d backoff=%s",
		c.interval, c.workers, c.backoff)
}

// Touch increments the desired version for a session and enqueues a reconcile.
// Idempotent: does nothing if session is already queued or in-flight.
func (c *Controller) Touch(sessionID, reason string) {
	if sessionID == "" {
		return
	}
	c.enqueue(sessionID, reason)
}

// Stats returns current runtime statistics.
func (c *Controller) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := "running"
	if !c.started {
		status = "not_started"
	}
	return Stats{
		Queued:   len(c.queued),
		InFlight: len(c.inFlight),
		Interval: c.interval.String(),
		Workers:  c.workers,
		Status:   status,
	}
}

// ─── Internal loops ───────────────────────────────────────────────────────────

func (c *Controller) enqueue(sessionID, reason string) {
	c.mu.Lock()
	if c.queued[sessionID] || c.inFlight[sessionID] {
		c.mu.Unlock()
		return
	}
	c.queued[sessionID] = true
	c.mu.Unlock()

	select {
	case c.jobs <- reconcileJob{sessionID: sessionID, reason: reason}:
	default:
		c.mu.Lock()
		delete(c.queued, sessionID)
		c.mu.Unlock()
		log.Printf("⚠️  controller queue full, dropped reconcile for session=%s reason=%s", sessionID, reason)
	}
}

func (c *Controller) workerLoop(workerID int) {
	for job := range c.jobs {
		c.mu.Lock()
		delete(c.queued, job.sessionID)
		if c.inFlight[job.sessionID] {
			c.mu.Unlock()
			continue
		}
		c.inFlight[job.sessionID] = true
		c.mu.Unlock()

		metricCyclesTotal.Inc()

		if err := c.reconcileSession(job); err != nil {
			log.Printf("❌ reconcile failed | worker=%d session=%s reason=%s err=%v",
				workerID, job.sessionID, job.reason, err)
			if markErr := c.store.MarkReconcileFailure(job.sessionID, err.Error()); markErr != nil {
				log.Printf("⚠️  mark failure error: %v", markErr)
			}
			backoff := c.backoff
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			time.AfterFunc(backoff, func() {
				c.enqueue(job.sessionID, "retry_backoff")
			})
		}

		c.mu.Lock()
		delete(c.inFlight, job.sessionID)
		c.mu.Unlock()
	}
}

func (c *Controller) bootstrapLoop() {
	sessions, err := c.store.ListActiveSessions()
	if err != nil {
		log.Printf("⚠️  controller bootstrap failed: %v", err)
		return
	}
	for _, sess := range sessions {
		c.enqueue(sess.ID, "startup_bootstrap")
	}
	log.Printf("🔁 Controller bootstrapped %d sessions", len(sessions))
}

func (c *Controller) periodicLoop() {
	for {
		jitter := time.Duration(rand.Int63n(int64(c.interval / 5)))
		time.Sleep(c.interval + jitter)

		sessions, err := c.store.ListActiveSessions()
		if err != nil {
			log.Printf("⚠️  periodic scan failed: %v", err)
			continue
		}
		for _, sess := range sessions {
			c.enqueue(sess.ID, "periodic_scan")
		}
	}
}

func (c *Controller) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		deleted, err := c.k8s.CleanupOrphanedPods()
		if err != nil {
			log.Printf("⚠️  cleanup loop error: %v", err)
			continue
		}
		if deleted > 0 {
			log.Printf("🧹 Cleanup removed %d orphaned pods", deleted)
		}
	}
}

// ─── Core reconciliation ──────────────────────────────────────────────────────

func (c *Controller) reconcileSession(job reconcileJob) error {
	start := time.Now()

	meta, err := c.store.GetReconcileMeta(job.sessionID)
	if err != nil {
		return fmt.Errorf("get reconcile meta: %w", err)
	}

	sess, err := c.store.GetSession(job.sessionID)
	if err != nil {
		// Session expired or deleted — converged terminal state, mark success.
		return c.store.MarkReconcileSuccess(job.sessionID, meta.DesiredVersion, time.Since(start), "session_gone")
	}

	switch sess.Status {
	case "running", "creating":
		if err := c.reconcileRunning(sess); err != nil {
			return err
		}
	case "terminating", "terminated", "expired", "failed":
		if err := c.reconcileTerminal(sess); err != nil {
			return err
		}
		// Session was deleted from Redis at the end of reconcileTerminal.
		// Stop here to avoid MarkReconcileSuccess on a non-existent session.
		return nil
	}

	version := meta.DesiredVersion
	if version == 0 {
		version = 1
	}
	if err := c.store.MarkReconcileSuccess(sess.ID, version, time.Since(start), job.reason); err != nil {
		return fmt.Errorf("mark success: %w", err)
	}

	log.Printf("✅ reconciled session=%s status=%s duration=%s reason=%s",
		sess.ID, sess.Status, time.Since(start).Round(time.Millisecond), job.reason)
	return nil
}

// reconcileRunning checks that a running session's pod and network grants are healthy.
func (c *Controller) reconcileRunning(sess state.Session) error {
	podStatus, err := c.k8s.GetPodStatus(sess.ID)
	if err != nil {
		return fmt.Errorf("get pod status: %w", err)
	}

	// Check TTL expiry.
	if time.Now().After(sess.ExpiresAt) {
		log.Printf("🕐 session %s expired, marking for termination", sess.ID)
		sess.Status = "expired"
		c.store.UpdateSession(sess)
		c.enqueue(sess.ID, "ttl_expired")
		return nil
	}

	if podStatus == "not_found" {
		log.Printf("⚠️  session %s pod missing, marking failed", sess.ID)
		sess.Status = "failed"
		c.store.UpdateSession(sess)
		metricRepairsTotal.Inc()
		return nil
	}

	// Re-apply network grants if node agent is available (idempotent).
	if c.agent != nil && sess.PodIP != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := c.agent.GrantPodAccess(ctx, sess.UserID, sess.PodIP); err != nil {
			if !isMissingClientErr(err) {
				metricRPCErrors.Inc()
				log.Printf("⚠️  reconcile GrantPodAccess(%s, %s): %v", sess.UserID, sess.PodIP, err)
			}
		}

		if sess.VpnIP != "" {
			if err := c.agent.EnsureUserIsolation(ctx, sess.UserID, sess.VpnIP); err != nil {
				if !isMissingClientErr(err) {
					metricRPCErrors.Inc()
					log.Printf("⚠️  reconcile EnsureUserIsolation(%s, %s): %v", sess.UserID, sess.VpnIP, err)
				}
			}
		}
	}

	return nil
}

// reconcileTerminal cleans up pods and revokes network grants for terminal sessions.
func (c *Controller) reconcileTerminal(sess state.Session) error {
	if c.agent != nil && sess.PodIP != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := c.agent.RevokePodAccess(ctx, sess.UserID, sess.PodIP); err != nil {
			if !isMissingClientErr(err) {
				metricRPCErrors.Inc()
				log.Printf("⚠️  reconcile RevokePodAccess: %v", err)
			}
		}
		if sess.VpnIP != "" {
			if err := c.agent.RevokeUserIsolation(ctx, sess.UserID, sess.VpnIP); err != nil {
				if !isMissingClientErr(err) {
					metricRPCErrors.Inc()
					log.Printf("⚠️  reconcile RevokeUserIsolation: %v", err)
				}
			}
		}
		c.store.DeleteGrant(sess.PodIP)
	}

	// Best-effort pod deletion (may already be gone).
	if err := c.k8s.TerminatePod(sess.ID); err != nil {
		log.Printf("⚠️  reconcile TerminatePod(%s): %v", sess.ID, err)
	}

	// FINAL STEP: Remove from Redis entirely so it stops appearing in active lists.
	if err := c.store.DeleteSession(sess.ID); err != nil {
		return fmt.Errorf("final session deletion failed: %w", err)
	}

	return nil
}

// isMissingClientErr returns true for non-fatal "no client" errors.
func isMissingClientErr(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}
