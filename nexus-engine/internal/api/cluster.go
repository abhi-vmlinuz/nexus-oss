package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterPodInfo is the JSON model for the Cluster tab.
type ClusterPodInfo struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Ready       string `json:"ready"`
	Restarts    int    `json:"restarts"`
	CPUUsage    string `json:"cpu_usage"`
	MemoryUsage string `json:"memory_usage"`
	AgeSeconds  int    `json:"age_seconds"`
	Error       string `json:"error"`
}

// ClusterNodeInfo is the JSON model for the Cluster tab.
type ClusterNodeInfo struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	CPUPercent    int    `json:"cpu_percent"`
	MemoryPercent int    `json:"memory_percent"`
	PodsReady     int    `json:"pods_ready"`
	PodsMax       int    `json:"pods_max"`
}

// NetworkPolicyInfo is the JSON model for the Cluster tab.
type NetworkPolicyInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
}

func (h *adminHandler) GetClusterPods(c *gin.Context) {
	if h.d.K8s == nil {
		c.JSON(http.StatusOK, gin.H{"pods": []any{}, "namespace": ""})
		return
	}

	clientset := h.d.K8s.Clientset()
	namespace := h.d.Cfg.K3sNamespace
	ctx := context.Background()

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var result = []ClusterPodInfo{}
	for _, p := range pods.Items {
		ready := "0/0"
		restarts := 0
		if len(p.Status.ContainerStatuses) > 0 {
			readyCount := 0
			for _, cs := range p.Status.ContainerStatuses {
				if cs.Ready {
					readyCount++
				}
				restarts += int(cs.RestartCount)
			}
			ready = fmt.Sprintf("%d/%d", readyCount, len(p.Status.ContainerStatuses))
		}

		reason := ""
		if p.Status.Phase == "Failed" || p.Status.Phase == "Unknown" {
			reason = p.Status.Reason
		}
		for _, cs := range p.Status.ContainerStatuses {
			if cs.State.Waiting != nil {
				reason = cs.State.Waiting.Reason
			} else if cs.State.Terminated != nil {
				reason = cs.State.Terminated.Reason
			}
		}

		result = append(result, ClusterPodInfo{
			Name:       p.Name,
			Status:     string(p.Status.Phase),
			Ready:      ready,
			Restarts:   restarts,
			CPUUsage:   "-", // Metrics-server integration would go here
			MemoryUsage: "-",
			AgeSeconds:  int(time.Since(p.CreationTimestamp.Time).Seconds()),
			Error:       reason,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"namespace": namespace,
		"pods":      result,
	})
}

func (h *adminHandler) GetClusterNodes(c *gin.Context) {
	if h.d.K8s == nil {
		c.JSON(http.StatusOK, gin.H{"nodes": []any{}})
		return
	}

	clientset := h.d.K8s.Clientset()
	ctx := context.Background()

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var result = []ClusterNodeInfo{}
	for _, n := range nodes.Items {
		status := "Unknown"
		for _, cond := range n.Status.Conditions {
			if cond.Type == "Ready" {
				if cond.Status == "True" {
					status = "Ready"
				} else {
					status = "NotReady"
				}
			}
		}

		result = append(result, ClusterNodeInfo{
			Name:          n.Name,
			Status:        status,
			CPUPercent:    0, // Metrics-server integration would go here
			MemoryPercent: 0,
			PodsReady:     0, // Would need to count pods on this node
			PodsMax:       int(n.Status.Allocatable.Pods().Value()),
		})
	}

	c.JSON(http.StatusOK, gin.H{"nodes": result})
}

func (h *adminHandler) GetNetworkPolicies(c *gin.Context) {
	if h.d.K8s == nil {
		c.JSON(http.StatusOK, gin.H{"policies": []NetworkPolicyInfo{}})
		return
	}

	clientset := h.d.K8s.Clientset()
	namespace := h.d.Cfg.K3sNamespace
	ctx := context.Background()

	policies, err := clientset.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var result = []NetworkPolicyInfo{}
	for _, p := range policies.Items {
		result = append(result, NetworkPolicyInfo{
			Name:      p.Name,
			Namespace: p.Namespace,
			Status:    "active",
		})
	}

	c.JSON(http.StatusOK, gin.H{"policies": result})
}
