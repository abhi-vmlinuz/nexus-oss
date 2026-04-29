package api

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
)

type challengeHandler struct{ d Deps }

func newChallengeHandler(d Deps) *challengeHandler { return &challengeHandler{d: d} }

// CreateChallengeRequest is the body for POST /api/v1/challenges.
type CreateChallengeRequest struct {
	Name           string               `json:"name" binding:"required"`
	DockerfilePath string               `json:"dockerfile_path"`
	ComposePath    string               `json:"compose_path"`
	TTLMinutes     int                  `json:"ttl_minutes"`
	Ports          []int                `json:"ports"`
	// Multi-container support.
	Containers     []state.ContainerSpec `json:"containers"`
}

// Create registers a new challenge by building its Docker image.
func (h *challengeHandler) Create(c *gin.Context) {
	var req CreateChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Default TTL from config.
	if req.TTLMinutes <= 0 {
		req.TTLMinutes = h.d.Cfg.Session.DefaultTTLMinutes
	}

	// Validate: must supply exactly one of dockerfile_path, compose_path, or containers[].
	provided := 0
	if req.DockerfilePath != "" { provided++ }
	if req.ComposePath != ""    { provided++ }
	if len(req.Containers) > 0  { provided++ }
	if provided == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "must supply one of: dockerfile_path, compose_path, or containers[]"})
		return
	}
	if provided > 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "supply only one of: dockerfile_path, compose_path, or containers[]"})
		return
	}

	// Generate deterministic ID from name.
	challengeID := fmt.Sprintf("%s-%s", sanitizeName(req.Name), uuid.New().String()[:8])

	var ch state.Challenge

	if req.ComposePath != "" {
		// ── Compose path: engine parses + builds all services ────────────────
		log.Printf("parsing compose file for challenge %s from %s", challengeID, req.ComposePath)
		parsed, err := h.d.Builder.ParseAndBuild(req.Name, req.ComposePath)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":   "COMPOSE_BUILD_FAILED",
				"message": err.Error(),
			})
			return
		}
		ch = state.Challenge{
			ID:         challengeID,
			Name:       req.Name,
			Containers: parsed.Containers,
			TTLMinutes: req.TTLMinutes,
			Ports:      parsed.AllPorts,
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}
		log.Printf("challenge %s registered with %d containers", challengeID, len(parsed.Containers))
	} else if len(req.Containers) > 0 {
		// ── Pre-built containers: no build step ──────────────────────────────
		log.Printf("registering multi-container challenge %s (%d containers)", challengeID, len(req.Containers))
		var allPorts []int
		seen := map[int]bool{}
		for _, ct := range req.Containers {
			for _, p := range ct.Ports {
				if !seen[p] { seen[p] = true; allPorts = append(allPorts, p) }
			}
		}
		ch = state.Challenge{
			ID:         challengeID,
			Name:       req.Name,
			Containers: req.Containers,
			TTLMinutes: req.TTLMinutes,
			Ports:      allPorts,
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}
	} else {
		// ── Single-container: build + push via nerdctl ──────────────────────
		log.Printf("building image for challenge %s from %s", challengeID, req.DockerfilePath)
		result, err := h.d.Builder.Build(req.Name, req.DockerfilePath)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":   "BUILD_FAILED",
				"message": err.Error(),
			})
			return
		}
		if len(req.Ports) == 0 {
			req.Ports = result.Ports
		}
		ch = state.Challenge{
			ID:             challengeID,
			Name:           req.Name,
			Image:          result.Image,
			DockerfilePath: req.DockerfilePath,
			TTLMinutes:     req.TTLMinutes,
			Ports:          req.Ports,
			Tag:            result.Tag,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}
		log.Printf("challenge %s registered: image=%s duration=%s", challengeID, result.Image, result.Duration)
	}

	if err := h.d.Store.SaveChallenge(ch); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, ch)
}

// List returns all registered challenges.
func (h *challengeHandler) List(c *gin.Context) {
	challenges, err := h.d.Store.ListChallenges()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"challenges": challenges, "count": len(challenges)})
}

// Get returns a single challenge by ID.
func (h *challengeHandler) Get(c *gin.Context) {
	ch, err := h.d.Store.GetChallenge(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	c.JSON(http.StatusOK, ch)
}

// Delete removes a challenge definition. Does not terminate existing sessions.
func (h *challengeHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.d.Store.GetChallenge(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err := h.d.Store.DeleteChallenge(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"challenge_id": id, "status": "deleted"})
}

// Rebuild triggers a fresh nerdctl build for an existing challenge.
func (h *challengeHandler) Rebuild(c *gin.Context) {
	ch, err := h.d.Store.GetChallenge(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	log.Printf("rebuilding challenge %s from %s", ch.ID, ch.DockerfilePath)
	result, err := h.d.Builder.Build(ch.Name, ch.DockerfilePath)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   "BUILD_FAILED",
			"message": err.Error(),
		})
		return
	}

	ch.Image = result.Image
	ch.Tag = result.Tag
	ch.UpdatedAt = time.Now().UTC()
	if err := h.d.Store.SaveChallenge(ch); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"challenge_id": ch.ID,
		"image":        result.Image,
		"duration_ms":  result.Duration.Milliseconds(),
		"status":       "rebuilt",
	})
}
