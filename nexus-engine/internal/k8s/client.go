// Package k8s wraps client-go for nexus-engine pod orchestration.
// All pods are created in the nexus-challenges namespace with standard labels.
package k8s

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	labelApp       = "app"
	labelAppValue  = "nexus-challenge"
	labelSessionID = "nexus-session-id"
	labelChallengeID = "nexus-challenge-id"
	labelUserID    = "nexus-user-id"
	annotationTTL  = "nexus.io/ttl"
	annotationCreatedAt = "nexus.io/created-at"
	podNamePrefix  = "chal-"
)

// ContainerSpec mirrors state.ContainerSpec — copied to avoid import cycle.
type ContainerSpec struct {
	Name  string
	Image string
	Ports []int
	Env   map[string]string
}

// SpawnRequest contains all info needed to create a challenge pod.
type SpawnRequest struct {
	SessionID   string
	ChallengeID string
	UserID      string
	// Single-container.
	Image string
	Ports []int
	// Multi-container (if set, Image is ignored).
	Containers []ContainerSpec
	TTLMinutes int
}

// PodInfo is returned after successful pod creation.
type PodInfo struct {
	PodName string
	PodIP   string
}

// ResourceInfo describes a single k8s resource for admin/debug views.
type ResourceInfo struct {
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	PodIP     string `json:"pod_ip,omitempty"`
	CreatedAt string `json:"created_at"`
}

// Client is the Kubernetes adapter for nexus-engine.
type Client struct {
	clientset *kubernetes.Clientset
	namespace string
}

// New creates a Client, auto-detecting in-cluster config then falling back to kubeconfig.
func New(namespace string) (*Client, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("k8s config: %w", err)
		}
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s clientset: %w", err)
	}

	// Ensure namespace exists.
	ctx := context.Background()
	if _, err := cs.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{}); err != nil {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   namespace,
			Labels: map[string]string{"managed-by": "nexus"},
		}}
		if _, err := cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("create namespace %q: %w", namespace, err)
		}
		log.Printf("created namespace %s", namespace)
	}
	return &Client{clientset: cs, namespace: namespace}, nil
}

func (c *Client) Clientset() *kubernetes.Clientset {
	return c.clientset
}

// SpawnPod creates a challenge pod and waits for it to receive a pod IP.
// Returns PodInfo with the pod name and cluster IP on success.
func (c *Client) SpawnPod(req SpawnRequest) (*PodInfo, error) {
	ctx := context.Background()
	podName := podNamePrefix + req.SessionID

	var podContainers []corev1.Container

	if len(req.Containers) > 0 {
		// ── Multi-container path ──────────────────────────────────────────────
		for _, ct := range req.Containers {
			ports := make([]corev1.ContainerPort, len(ct.Ports))
			for i, p := range ct.Ports {
				ports[i] = corev1.ContainerPort{ContainerPort: int32(p), Protocol: corev1.ProtocolTCP}
			}
			var envVars []corev1.EnvVar
			for k, v := range ct.Env {
				envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
			}
			podContainers = append(podContainers, corev1.Container{
				Name:            sanitizeK8sName(ct.Name),
				Image:           ct.Image,
				Ports:           ports,
				Env:             envVars,
				// IfNotPresent: allows public images (redis, postgres) to be pulled by kubelet.
				// Local registry images are already in containerd via nerdctl build.
				ImagePullPolicy: corev1.PullIfNotPresent,
			})
		}
	} else {
		// ── Single-container path (unchanged) ────────────────────────────────
		ports := make([]corev1.ContainerPort, len(req.Ports))
		for i, p := range req.Ports {
			ports[i] = corev1.ContainerPort{ContainerPort: int32(p), Protocol: corev1.ProtocolTCP}
		}
		podContainers = []corev1.Container{{
			Name:            "challenge",
			Image:           req.Image,
			Ports:           ports,
			ImagePullPolicy: corev1.PullIfNotPresent,
		}}
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: c.namespace,
			Labels: map[string]string{
				labelApp:         labelAppValue,
				labelSessionID:   req.SessionID,
				labelChallengeID: req.ChallengeID,
				labelUserID:      req.UserID,
			},
			Annotations: map[string]string{
				annotationTTL:       fmt.Sprintf("%dm", req.TTLMinutes),
				annotationCreatedAt: time.Now().UTC().Format(time.RFC3339),
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers:    podContainers,
		},
	}

	if _, err := c.clientset.CoreV1().Pods(c.namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create pod: %w", err)
	}

	// Wait up to 90s for pod IP.
	for i := 0; i < 90; i++ {
		p, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, podName, metav1.GetOptions{})
		if err == nil && p.Status.PodIP != "" {
			log.Printf("pod %s ready: ip=%s containers=%d", podName, p.Status.PodIP, len(podContainers))
			return &PodInfo{PodName: podName, PodIP: p.Status.PodIP}, nil
		}
		time.Sleep(time.Second)
	}

	// Cleanup on timeout.
	c.clientset.CoreV1().Pods(c.namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	return nil, fmt.Errorf("pod %s did not receive IP within 90s", podName)
}

// TerminatePod deletes a challenge pod. Idempotent — returns nil if not found.
func (c *Client) TerminatePod(sessionID string) error {
	ctx := context.Background()
	podName := podNamePrefix + sessionID
	err := c.clientset.CoreV1().Pods(c.namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("delete pod %s: %w", podName, err)
	}
	return nil
}

// GetPodStatus returns the Kubernetes pod phase string.
// Returns "not_found" if the pod does not exist.
func (c *Client) GetPodStatus(sessionID string) (string, error) {
	ctx := context.Background()
	pod, err := c.clientset.CoreV1().Pods(c.namespace).Get(ctx, podNamePrefix+sessionID, metav1.GetOptions{})
	if err != nil {
		if isNotFound(err) {
			return "not_found", nil
		}
		return "", err
	}
	return string(pod.Status.Phase), nil
}

// ExtendPodTTL updates the TTL annotation on a running pod.
func (c *Client) ExtendPodTTL(sessionID string, newTotalMinutes int) error {
	ctx := context.Background()
	patch := fmt.Sprintf(`{"metadata":{"annotations":{%q:%q}}}`,
		annotationTTL, fmt.Sprintf("%dm", newTotalMinutes))
	_, err := c.clientset.CoreV1().Pods(c.namespace).Patch(
		ctx, podNamePrefix+sessionID, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// ListPods returns all nexus-managed pods (filtered by app=nexus-challenge).
func (c *Client) ListPods() ([]ResourceInfo, error) {
	ctx := context.Background()
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", labelApp, labelAppValue),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ResourceInfo, 0, len(pods.Items))
	for _, p := range pods.Items {
		out = append(out, ResourceInfo{
			SessionID: p.Labels[labelSessionID],
			Type:      "pod",
			Name:      p.Name,
			Status:    string(p.Status.Phase),
			PodIP:     p.Status.PodIP,
			CreatedAt: p.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

// CleanupOrphanedPods deletes pods with expired TTL annotations or in terminal phases.
// Returns the count of pods deleted.
func (c *Client) CleanupOrphanedPods() (int, error) {
	ctx := context.Background()
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", labelApp, labelAppValue),
	})
	if err != nil {
		return 0, err
	}

	deleted := 0
	for _, p := range pods.Items {
		if shouldClean(p) {
			if err := c.clientset.CoreV1().Pods(c.namespace).Delete(ctx, p.Name, metav1.DeleteOptions{}); err == nil {
				deleted++
				log.Printf("cleaned orphaned pod %s", p.Name)
			}
		}
	}
	return deleted, nil
}

// shouldClean returns true if the pod should be garbage-collected.
func shouldClean(p corev1.Pod) bool {
	if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
		return true
	}
	ttlStr := p.Annotations[annotationTTL]
	if ttlStr != "" {
		if d, err := time.ParseDuration(ttlStr); err == nil {
			if time.Since(p.CreationTimestamp.Time) > d+5*time.Minute {
				return true
			}
		}
	}
	// Hard ceiling: 3 hours.
	return time.Since(p.CreationTimestamp.Time) > 3*time.Hour
}

// sanitizeK8sName converts arbitrary strings to RFC-1123 compliant names.
func sanitizeK8sName(name string) string {
	r := strings.ToLower(name)
	r = strings.ReplaceAll(r, "_", "-")
	r = strings.ReplaceAll(r, " ", "-")
	var b strings.Builder
	for _, c := range r {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		}
	}
	return strings.Trim(b.String(), "-")
}

// isNotFound checks for k8s 404 errors without importing k8s error helpers.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}
