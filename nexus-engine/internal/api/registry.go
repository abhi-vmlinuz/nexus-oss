package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type RegistryImageInfo struct {
	Name      string    `json:"name"`
	Tags      []string  `json:"tags"`
	SizeMB    int       `json:"size_mb"`
	CreatedAt time.Time `json:"created_at"`
}

type RegistryStats struct {
	TotalImages    int    `json:"total_images"`
	TotalStorageMB int    `json:"total_storage_mb"`
	OrphanedImages int    `json:"orphaned_images"`
	MostUsedImage  string `json:"most_used_image"`
	MostUsedRefs   int    `json:"most_used_refs"`
}

type RegistryPullInfo struct {
	Image       string  `json:"image"`
	Pulls       int     `json:"pulls"`
	SuccessRate float64 `json:"success_rate"`
}

func (h *adminHandler) GetRegistryImages(c *gin.Context) {
	regURL := h.d.Cfg.Registry.URL
	if regURL == "" {
		regURL = "localhost:5000"
	}

	// For local registry, query the catalog
	resp, err := http.Get(fmt.Sprintf("http://%s/v2/_catalog", regURL))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"registry_type": "local",
			"registry_url":  regURL,
			"connected":     false,
			"images":        []any{},
			"error":         err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode registry catalog"})
		return
	}

	var images []RegistryImageInfo
	for _, repo := range catalog.Repositories {
		// Get tags for each repo
		tagResp, err := http.Get(fmt.Sprintf("http://%s/v2/%s/tags/list", regURL, repo))
		if err == nil {
			var tags struct {
				Tags []string `json:"tags"`
			}
			json.NewDecoder(tagResp.Body).Decode(&tags)
			tagResp.Body.Close()
			
			images = append(images, RegistryImageInfo{
				Name:      repo,
				Tags:      tags.Tags,
				SizeMB:    0, // Placeholder
				CreatedAt: time.Now().Add(-24 * time.Hour), // Placeholder
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"registry_type": "local",
		"registry_url":  regURL,
		"connected":     true,
		"images":        images,
	})
}

func (h *adminHandler) GetRegistryStats(c *gin.Context) {
	// Mock stats for now as calculating storage requires deep registry inspection
	c.JSON(http.StatusOK, RegistryStats{
		TotalImages:    10,
		TotalStorageMB: 1200,
		OrphanedImages: 2,
		MostUsedImage:  "pwn-101",
		MostUsedRefs:   5,
	})
}

func (h *adminHandler) GetRegistryPulls(c *gin.Context) {
	// In a real implementation, this would query a metrics store or the registry logs
	c.JSON(http.StatusOK, gin.H{
		"pulls_last_hour": []RegistryPullInfo{
			{Image: "pwn-101", Pulls: 15, SuccessRate: 100},
			{Image: "nexus-registry", Pulls: 2, SuccessRate: 100},
		},
	})
}
