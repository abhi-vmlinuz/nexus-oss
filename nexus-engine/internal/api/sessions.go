package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nexus-oss/nexus/nexus-engine/internal/k8s"
	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
)

type sessionHandler struct{ d Deps }

func newSessionHandler(d Deps) *sessionHandler { return &sessionHandler{d: d} }

// CreateSessionRequest is the body for POST /api/v1/sessions.
type CreateSessionRequest struct {
	ChallengeID string `json:"challenge_id" binding:"required"`
	// UserID is required — used for VPN-based network isolation.
	UserID string `json:"user_id" binding:"required"`
	// VpnIP is the WireGuard-assigned IP for this user.
	// Required in prod mode for EnsureUserIsolation; ignored in dev mode.
	VpnIP string `json:"vpn_ip,omitempty"`
}

// Create spawns a new challenge pod and returns the pod IP.
//
// Flow:
//  1. Validate challenge exists
//  2. Enforce per-user session limit (if configured)
//  3. Create pod in k3s
//  4. Call node agent: GrantPodAccess + EnsureUserIsolation (prod mode)
//  5. Save session to Redis
//  6. Touch desired version → enqueue reconcile
func (h *sessionHandler) Create(c *gin.Context) {
	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate challenge.
	ch, err := h.d.Store.GetChallenge(req.ChallengeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "CHALLENGE_NOT_FOUND",
			"message": fmt.Sprintf("challenge %q does not exist", req.ChallengeID),
		})
		return
	}

	// Enforce session limit if configured.
	if h.d.Cfg.Session.MaxPerUser > 0 {
		count, _ := h.d.Store.CountUserActiveSessions(req.UserID)
		if count >= h.d.Cfg.Session.MaxPerUser {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "SESSION_LIMIT_REACHED",
				"message": fmt.Sprintf("user %q already has %d session(s), limit is %d", req.UserID, count, h.d.Cfg.Session.MaxPerUser),
			})
			return
		}
	}

	// Prod mode: vpn_ip is required for isolation.
	if h.d.Cfg.IsProd() && req.VpnIP == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "VPN_IP_REQUIRED",
			"message": "vpn_ip is required in prod mode for network isolation",
		})
		return
	}

	sessionID := fmt.Sprintf("sess-%s", uuid.New().String()[:8])
	log.Printf("session %s: creating pod for user=%s challenge=%s", sessionID, req.UserID, req.ChallengeID)

	// Build the spawn request, branching on challenge type.
	spawnReq := k8s.SpawnRequest{
		SessionID:   sessionID,
		ChallengeID: req.ChallengeID,
		UserID:      req.UserID,
		TTLMinutes:  ch.TTLMinutes,
	}
	if ch.IsMultiContainer() {
		for _, ct := range ch.Containers {
			spawnReq.Containers = append(spawnReq.Containers, k8s.ContainerSpec{
				Name:           ct.Name,
				Image:          ct.Image,
				Ports:          ct.Ports,
				Env:            ct.Env,
				Resources:      h.mapToK8sResources(ct.Resources),
				ReadinessProbe: h.mapToK8sProbe(ct.ReadinessProbe),
			})
		}
	} else {
		spawnReq.Image = ch.Image
		spawnReq.Ports = ch.Ports
		spawnReq.Resources = h.mapToK8sResources(ch.Resources)
		spawnReq.ReadinessProbe = h.mapToK8sProbe(ch.ReadinessProbe)
	}

	// Spawn pod.
	podInfo, err := h.d.K8s.SpawnPod(spawnReq)
	if err != nil {
		log.Printf("session %s: pod spawn failed: %v", sessionID, err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "POD_SPAWN_FAILED",
			"message": err.Error(),
		})
		return
	}

	// Build session record.
	now := time.Now().UTC()
	sess := state.Session{
		ID:          sessionID,
		UserID:      req.UserID,
		ChallengeID: req.ChallengeID,
		PodName:     podInfo.PodName,
		PodIP:       podInfo.PodIP,
		Status:      "running",
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Duration(ch.TTLMinutes) * time.Minute),
		VpnIP:       req.VpnIP,
	}

	// Grant network access via node agent (prod mode only).
	if h.d.NodeAgent != nil && podInfo.PodIP != "" {
		ctx := context.Background()

		if err := h.d.NodeAgent.GrantPodAccess(ctx, req.UserID, podInfo.PodIP); err != nil {
			log.Printf("session %s: GrantPodAccess failed (non-fatal): %v", sessionID, err)
		}

		if req.VpnIP != "" {
			if err := h.d.NodeAgent.EnsureUserIsolation(ctx, req.UserID, req.VpnIP); err != nil {
				log.Printf("session %s: EnsureUserIsolation failed (non-fatal): %v", sessionID, err)
			}
		}

		// Save grant record.
		h.d.Store.SaveGrant(state.GrantRecord{
			SessionID: sessionID,
			UserID:    req.UserID,
			PodIP:     podInfo.PodIP,
			Status:    "applied",
			GrantedAt: now,
		})
	}

	// Persist session.
	if err := h.d.Store.SaveSession(sess); err != nil {
		// Best-effort cleanup: delete the pod we just created.
		h.d.K8s.TerminatePod(sessionID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save session"})
		return
	}

	// Enqueue reconciliation.
	if _, err := h.d.Store.TouchDesiredVersion(sessionID, "session_create"); err != nil {
		log.Printf("session %s: touch desired version failed: %v", sessionID, err)
	}
	h.d.Controller.Touch(sessionID, "session_create")

	log.Printf("session %s: created | pod_ip=%s", sessionID, podInfo.PodIP)

	type ServiceInfo struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	}
	var services []ServiceInfo
	if ch.IsMultiContainer() {
		for _, ct := range ch.Containers {
			for _, p := range ct.Ports {
				services = append(services, ServiceInfo{Name: ct.Name, Port: p})
			}
		}
	} else {
		for _, p := range ch.Ports {
			services = append(services, ServiceInfo{Name: "main", Port: p})
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"session_id":   sess.ID,
		"user_id":      sess.UserID,
		"challenge_id": sess.ChallengeID,
		"pod_ip":       sess.PodIP,
		"pod_name":     sess.PodName,
		"status":       sess.Status,
		"created_at":   sess.CreatedAt,
		"expires_at":   sess.ExpiresAt,
		"ports":        ch.AllPorts(),
		"services":     services,
	})
}

func (h *sessionHandler) mapToK8sResources(r *state.Resources) *k8s.Resources {
	cpu := h.d.Cfg.Challenge.DefaultCPULimit
	mem := h.d.Cfg.Challenge.DefaultMemoryLimit

	if r != nil {
		if r.CPU != "" {
			cpu = r.CPU
		}
		if r.Memory != "" {
			mem = r.Memory
		}
	}

	return &k8s.Resources{
		CPU:    cpu,
		Memory: mem,
	}
}

func (h *sessionHandler) mapToK8sProbe(p *state.ReadinessProbe) *k8s.ReadinessProbe {
	if p == nil {
		return nil
	}
	probe := &k8s.ReadinessProbe{
		InitialDelaySeconds: p.InitialDelaySeconds,
		PeriodSeconds:       p.PeriodSeconds,
		TimeoutSeconds:      p.TimeoutSeconds,
		FailureThreshold:    p.FailureThreshold,
	}
	if p.HTTPGet != nil {
		probe.HTTPGet = &k8s.HTTPGetAction{Path: p.HTTPGet.Path, Port: p.HTTPGet.Port}
	}
	if p.TCPSocket != nil {
		probe.TCPSocket = &k8s.TCPSocketAction{Port: p.TCPSocket.Port}
	}
	if p.Exec != nil {
		probe.Exec = &k8s.ExecAction{Command: p.Exec.Command}
	}
	return probe
}

// List returns all active sessions. In admin context all sessions; for user context filtered.
func (h *sessionHandler) List(c *gin.Context) {
	sessions, err := h.d.Store.ListAllSessions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions, "count": len(sessions)})
}

// Get returns a single session by ID.
func (h *sessionHandler) Get(c *gin.Context) {
	sess, err := h.d.Store.GetSession(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	c.JSON(http.StatusOK, sess)
}

// Terminate deletes the pod and marks the session terminated.
func (h *sessionHandler) Terminate(c *gin.Context) {
	id := c.Param("id")
	sess, err := h.d.Store.GetSession(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	// Revoke network access.
	if h.d.NodeAgent != nil && sess.PodIP != "" {
		ctx := context.Background()
		if err := h.d.NodeAgent.RevokePodAccess(ctx, sess.UserID, sess.PodIP); err != nil {
			log.Printf("session %s: RevokePodAccess failed (non-fatal): %v", id, err)
		}
		if sess.VpnIP != "" {
			if err := h.d.NodeAgent.RevokeUserIsolation(ctx, sess.UserID, sess.VpnIP); err != nil {
				log.Printf("session %s: RevokeUserIsolation failed (non-fatal): %v", id, err)
			}
		}
		h.d.Store.DeleteGrant(sess.PodIP)
	}

	// Terminate pod.
	if err := h.d.K8s.TerminatePod(id); err != nil {
		log.Printf("session %s: TerminatePod failed (non-fatal): %v", id, err)
	}

	// Update session status.
	sess.Status = "terminated"
	h.d.Store.UpdateSession(sess)

	if _, err := h.d.Store.TouchDesiredVersion(id, "session_terminate"); err != nil {
		log.Printf("session %s: touch desired version failed: %v", id, err)
	}
	h.d.Controller.Touch(id, "session_terminate")

	c.JSON(http.StatusOK, gin.H{"session_id": id, "status": "terminated"})
}

// ExtendRequest is the body for POST /api/v1/sessions/:id/extend.
type ExtendRequest struct {
	DurationMinutes int `json:"duration_minutes"`
}

// Extend adds time to a session's TTL.
func (h *sessionHandler) Extend(c *gin.Context) {
	id := c.Param("id")
	sess, err := h.d.Store.GetSession(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	var req ExtendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.DurationMinutes <= 0 {
		req.DurationMinutes = h.d.Cfg.Session.DefaultTTLMinutes
	}

	oldExpiry := sess.ExpiresAt
	if err := h.d.Store.ExtendSession(id, req.DurationMinutes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Update pod TTL annotation.
	remaining := int(time.Until(oldExpiry).Minutes()) + req.DurationMinutes
	if err := h.d.K8s.ExtendPodTTL(id, remaining); err != nil {
		log.Printf("session %s: ExtendPodTTL failed (non-fatal): %v", id, err)
	}

	if _, err := h.d.Store.TouchDesiredVersion(id, "session_extend"); err != nil {
		log.Printf("session %s: touch desired version failed: %v", id, err)
	}

	c.JSON(http.StatusOK, gin.H{
		"session_id":     id,
		"status":         sess.Status,
		"old_expires_at": oldExpiry.Format(time.RFC3339),
		"new_expires_at": oldExpiry.Add(time.Duration(req.DurationMinutes) * time.Minute).Format(time.RFC3339),
	})
}
