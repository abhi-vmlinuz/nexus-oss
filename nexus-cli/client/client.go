// Package client provides a typed HTTP client for nexus-engine.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a typed HTTP client for nexus-engine.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a new Client targeting the given engine base URL.
func New(engineURL string) *Client {
	return &Client{
		baseURL: engineURL,
		httpClient: &http.Client{
			// Compose builds can take several minutes; use a generous timeout.
			Timeout: 10 * time.Minute,
		},
	}
}

// ─── Response types ───────────────────────────────────────────────────────────

type Challenge struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Image          string          `json:"image,omitempty"`
	DockerfilePath string          `json:"dockerfile_path,omitempty"`
	TTLMinutes     int             `json:"ttl_minutes"`
	Ports          []int           `json:"ports"`
	Tag            string          `json:"tag,omitempty"`
	Containers     []ContainerSpec `json:"containers,omitempty"`
	Resources      *Resources      `json:"resources,omitempty"`
	ReadinessProbe *ReadinessProbe `json:"readiness_probe,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// ContainerSpec describes a single container in a multi-container challenge.
type ContainerSpec struct {
	Name           string            `json:"name"`
	Image          string            `json:"image"`
	Ports          []int             `json:"ports,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Resources      *Resources        `json:"resources,omitempty"`
	ReadinessProbe *ReadinessProbe   `json:"readiness_probe,omitempty"`
}

type Resources struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

type ReadinessProbe struct {
	HTTPGet             *HTTPGetAction   `json:"http_get,omitempty"`
	TCPSocket           *TCPSocketAction `json:"tcp_socket,omitempty"`
	Exec                *ExecAction      `json:"exec,omitempty"`
	InitialDelaySeconds int              `json:"initial_delay_seconds,omitempty"`
	PeriodSeconds       int              `json:"period_seconds,omitempty"`
	TimeoutSeconds      int              `json:"timeout_seconds,omitempty"`
	FailureThreshold    int              `json:"failure_threshold,omitempty"`
}

type HTTPGetAction struct {
	Path string `json:"path"`
	Port int    `json:"port"`
}

type TCPSocketAction struct {
	Port int `json:"port"`
}

type ExecAction struct {
	Command []string `json:"command"`
}

type Service struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

type Session struct {
	ID          string    `json:"session_id"`
	UserID      string    `json:"user_id"`
	ChallengeID string    `json:"challenge_id"`
	PodName     string    `json:"pod_name"`
	PodIP       string    `json:"pod_ip"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	VpnIP       string    `json:"vpn_ip,omitempty"`
	Ports       []int     `json:"ports,omitempty"`
	Services    []Service `json:"services,omitempty"`

	// Reconcile info (from debug endpoint).
	LastReconciledAt         time.Time `json:"last_reconciled_at,omitempty"`
	LastReconciledDurationMs int64     `json:"last_reconciled_duration_ms,omitempty"`
	LastReconcileError       string    `json:"last_reconcile_error,omitempty"`
}

type HealthResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Mode      string `json:"mode"`
	Timestamp string `json:"timestamp"`
}

type SystemInfo struct {
	SessionsTotal int    `json:"sessions_total"`
	PodsTotal     int    `json:"pods_total"`
	Mode          string `json:"mode"`
	Registry      string `json:"registry"`
	Timestamp     string `json:"timestamp"`
}

type ControllerStats struct {
	Queued   int    `json:"queued"`
	InFlight int    `json:"in_flight"`
	Interval string `json:"reconcile_interval"`
	Workers  int    `json:"workers"`
	Status   string `json:"status"`
}

// ─── Cluster Types ──────────────────────────────────────────────────────────

type ClusterPod struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Ready       string `json:"ready"`
	Restarts    int    `json:"restarts"`
	CPUUsage    string `json:"cpu_usage"`
	MemoryUsage string `json:"memory_usage"`
	AgeSeconds  int    `json:"age_seconds"`
	Error       string `json:"error"`
}

type ClusterNode struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	CPUPercent    int    `json:"cpu_percent"`
	MemoryPercent int    `json:"memory_percent"`
	PodsReady     int    `json:"pods_ready"`
	PodsMax       int    `json:"pods_max"`
}

type NetworkPolicy struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
}

// ─── Registry Types ─────────────────────────────────────────────────────────

type RegistryImage struct {
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

type RegistryPull struct {
	Image       string  `json:"image"`
	Pulls       int     `json:"pulls"`
	SuccessRate float64 `json:"success_rate"`
}

// ─── API calls ────────────────────────────────────────────────────────────────

func (c *Client) Health() (*HealthResponse, error) {
	var resp HealthResponse
	return &resp, c.get("/health", &resp)
}

func (c *Client) ListChallenges() ([]Challenge, error) {
	var resp struct {
		Challenges []Challenge `json:"challenges"`
	}
	return resp.Challenges, c.get("/api/v1/challenges", &resp)
}

func (c *Client) GetChallenge(id string) (*Challenge, error) {
	var resp Challenge
	return &resp, c.get("/api/v1/challenges/"+id, &resp)
}

type RegisterChallengeRequest struct {
	Name           string          `json:"name"`
	DockerfilePath string          `json:"dockerfile_path,omitempty"`
	ComposePath    string          `json:"compose_path,omitempty"`
	TTLMinutes     int             `json:"ttl_minutes,omitempty"`
	Ports          []int           `json:"ports,omitempty"`
	Containers     []ContainerSpec `json:"containers,omitempty"`
	Resources      *Resources      `json:"resources,omitempty"`
	ReadinessProbe *ReadinessProbe `json:"readiness_probe,omitempty"`
}

func (c *Client) RegisterChallenge(req RegisterChallengeRequest) (*Challenge, error) {
	var resp Challenge
	return &resp, c.post("/api/v1/challenges", req, &resp)
}

func (c *Client) DeleteChallenge(id string) error {
	return c.delete("/api/v1/challenges/" + id)
}

func (c *Client) RebuildChallenge(id string) (map[string]any, error) {
	var resp map[string]any
	return resp, c.post("/api/v1/challenges/"+id+"/rebuild", nil, &resp)
}

func (c *Client) ListSessions() ([]Session, error) {
	var resp struct {
		Sessions []Session `json:"sessions"`
	}
	return resp.Sessions, c.get("/api/v1/sessions", &resp)
}

func (c *Client) GetSession(id string) (*Session, error) {
	var resp Session
	return &resp, c.get("/api/v1/sessions/"+id, &resp)
}

type CreateSessionRequest struct {
	ChallengeID string `json:"challenge_id"`
	UserID      string `json:"user_id"`
	VpnIP       string `json:"vpn_ip,omitempty"`
}

func (c *Client) CreateSession(req CreateSessionRequest) (*Session, error) {
	var resp Session
	return &resp, c.post("/api/v1/sessions", req, &resp)
}

func (c *Client) TerminateSession(id string) error {
	return c.delete("/api/v1/sessions/" + id)
}

type ExtendSessionRequest struct {
	DurationMinutes int `json:"duration_minutes"`
}

func (c *Client) ExtendSession(id string, minutes int) (map[string]any, error) {
	var resp map[string]any
	return resp, c.post("/api/v1/sessions/"+id+"/extend", ExtendSessionRequest{DurationMinutes: minutes}, &resp)
}

func (c *Client) SystemInfo() (*SystemInfo, error) {
	var resp SystemInfo
	return &resp, c.get("/debug/system", &resp)
}

func (c *Client) ControllerStats() (*ControllerStats, error) {
	var resp ControllerStats
	return &resp, c.get("/debug/controller", &resp)
}

func (c *Client) AdminSessions() ([]Session, error) {
	var resp struct {
		Sessions []Session `json:"sessions"`
	}
	return resp.Sessions, c.get("/api/v1/admin/sessions", &resp)
}

func (c *Client) ClusterHealth() (map[string]any, error) {
	var resp map[string]any
	return resp, c.get("/api/v1/admin/cluster/health", &resp)
}

func (c *Client) TriggerReconcile() (map[string]any, error) {
	var resp map[string]any
	return resp, c.post("/api/v1/admin/reconcile", nil, &resp)
}

func (c *Client) GetClusterPods() (string, []ClusterPod, error) {
	var resp struct {
		Namespace string       `json:"namespace"`
		Pods      []ClusterPod `json:"pods"`
	}
	err := c.get("/api/v1/admin/cluster/pods", &resp)
	return resp.Namespace, resp.Pods, err
}

func (c *Client) GetClusterNodes() ([]ClusterNode, error) {
	var resp struct {
		Nodes []ClusterNode `json:"nodes"`
	}
	err := c.get("/api/v1/admin/cluster/nodes", &resp)
	return resp.Nodes, err
}

func (c *Client) GetNetworkPolicies() ([]NetworkPolicy, error) {
	var resp struct {
		Policies []NetworkPolicy `json:"policies"`
	}
	err := c.get("/api/v1/admin/cluster/network-policies", &resp)
	return resp.Policies, err
}

func (c *Client) GetRegistryImages() ([]RegistryImage, error) {
	var resp struct {
		Images []RegistryImage `json:"images"`
	}
	err := c.get("/api/v1/admin/registry/images", &resp)
	return resp.Images, err
}

func (c *Client) GetRegistryStats() (*RegistryStats, error) {
	var resp RegistryStats
	err := c.get("/api/v1/admin/registry/stats", &resp)
	return &resp, err
}

func (c *Client) GetRegistryPulls() ([]RegistryPull, error) {
	var resp struct {
		Pulls []RegistryPull `json:"pulls_last_hour"`
	}
	err := c.get("/api/v1/admin/registry/pulls", &resp)
	return resp.Pulls, err
}

func (c *Client) RawMetrics() (string, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/metrics")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func (c *Client) get(path string, out any) error {
	resp, err := c.httpClient.Get(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.decode(resp, out)
}

func (c *Client) post(path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
	} else {
		r = bytes.NewReader([]byte("{}"))
	}
	resp, err := c.httpClient.Post(c.baseURL+path, "application/json", r)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.decode(resp, out)
}

func (c *Client) delete(path string) error {
	req, _ := http.NewRequest(http.MethodDelete, c.baseURL+path, nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DELETE %s: %s %s", path, resp.Status, string(body))
	}
	return nil
}

func (c *Client) decode(resp *http.Response, out any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var apiErr struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &apiErr)
		msg := apiErr.Message
		if msg == "" {
			msg = apiErr.Error
		}
		if msg == "" {
			msg = string(body)
		}
		return fmt.Errorf("API error %d: %s", resp.StatusCode, msg)
	}
	return json.Unmarshal(body, out)
}
