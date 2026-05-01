// Package state manages all persistent state for nexus-engine via Redis.
// Key schema:
//
//	session:<id>             → Session JSON
//	session:<id>:desired     → desired version counter
//	session:<id>:observed    → ReconcileMeta JSON
//	challenge:<id>           → Challenge JSON
//	active_sessions          → set of session IDs
//	user_sessions:<user_id>  → set of session IDs
//	grant:pod:<pod_ip>       → GrantRecord JSON
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

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

// Challenge is registered by CTF devs via POST /api/v1/challenges.
type Challenge struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	// Single-container fields (mutually exclusive with Containers).
	Image          string          `json:"image,omitempty"`
	DockerfilePath string          `json:"dockerfile_path,omitempty"`
	Tag            string          `json:"tag,omitempty"`
	Resources      *Resources      `json:"resources,omitempty"`
	ReadinessProbe *ReadinessProbe `json:"readiness_probe,omitempty"`
	// Multi-container fields.
	Containers     []ContainerSpec `json:"containers,omitempty"`
	// Common fields.
	TTLMinutes     int             `json:"ttl_minutes"`
	Ports          []int           `json:"ports"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// IsMultiContainer returns true when the challenge has multiple containers.
func (c Challenge) IsMultiContainer() bool { return len(c.Containers) > 0 }

// AllPorts returns a deduplicated list of all exposed ports across all containers.
func (c Challenge) AllPorts() []int {
	if !c.IsMultiContainer() {
		return c.Ports
	}
	seen := map[int]bool{}
	var out []int
	for _, ct := range c.Containers {
		for _, p := range ct.Ports {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// Session represents a running challenge instance.
type Session struct {
	ID          string    `json:"session_id"`
	UserID      string    `json:"user_id"`
	ChallengeID string    `json:"challenge_id"`
	PodName     string    `json:"pod_name"`
	PodIP       string    `json:"pod_ip"`
	Status      string    `json:"status"` // creating|running|terminating|terminated|expired|failed
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	VpnIP       string    `json:"vpn_ip,omitempty"`
	// Reconcile bookkeeping.
	ReconcileVersion         int64     `json:"reconcile_version"`
	LastReconciledVersion    int64     `json:"last_reconciled_version"`
	LastReconciledAt         time.Time `json:"last_reconciled_at,omitempty"`
	LastReconciledDurationMs int64     `json:"last_reconciled_duration_ms,omitempty"`
	LastReconcileError       string    `json:"last_reconcile_error,omitempty"`
}

// ReconcileMeta is the observed state stored per-session.
type ReconcileMeta struct {
	DesiredVersion int64     `json:"desired_version"`
	LastReconciled time.Time `json:"last_reconciled"`
	Reason         string    `json:"reason"`
}

// GrantRecord tracks a network access grant for a pod IP.
type GrantRecord struct {
	SessionID string    `json:"session_id"`
	UserID    string    `json:"user_id"`
	PodIP     string    `json:"pod_ip"`
	Status    string    `json:"status"` // applied|pending_revoke|revoked
	GrantedAt time.Time `json:"granted_at"`
}

// Store handles all Redis operations for nexus-engine.
type Store struct {
	client *redis.Client
	ctx    context.Context
}

// New creates a Store from a Redis URL and verifies connectivity.
func New(redisURL string) (*Store, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid redis URL %q: %w", redisURL, err)
	}
	client := redis.NewClient(opts)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return &Store{client: client, ctx: ctx}, nil
}

// Close releases the Redis connection.
func (s *Store) Close() error { return s.client.Close() }

// Ping checks Redis connectivity.
func (s *Store) Ping() error { return s.client.Ping(s.ctx).Err() }

// ─── Challenge operations ─────────────────────────────────────────────────────

func (s *Store) SaveChallenge(ch Challenge) error {
	data, err := json.Marshal(ch)
	if err != nil {
		return err
	}
	if err := s.client.Set(s.ctx, fmt.Sprintf("challenge:%s", ch.ID), data, 0).Err(); err != nil {
		return fmt.Errorf("save challenge: %w", err)
	}
	s.client.SAdd(s.ctx, "challenges", ch.ID)
	return nil
}

func (s *Store) GetChallenge(id string) (Challenge, error) {
	data, err := s.client.Get(s.ctx, fmt.Sprintf("challenge:%s", id)).Result()
	if err == redis.Nil {
		return Challenge{}, fmt.Errorf("challenge %q not found", id)
	}
	if err != nil {
		return Challenge{}, err
	}
	var ch Challenge
	return ch, json.Unmarshal([]byte(data), &ch)
}

func (s *Store) ListChallenges() ([]Challenge, error) {
	ids, err := s.client.SMembers(s.ctx, "challenges").Result()
	if err != nil {
		return nil, err
	}
	out := make([]Challenge, 0, len(ids))
	for _, id := range ids {
		if ch, err := s.GetChallenge(id); err == nil {
			out = append(out, ch)
		}
	}
	return out, nil
}

func (s *Store) DeleteChallenge(id string) error {
	s.client.Del(s.ctx, fmt.Sprintf("challenge:%s", id))
	s.client.SRem(s.ctx, "challenges", id)
	return nil
}

// ─── Session operations ───────────────────────────────────────────────────────

func (s *Store) SaveSession(sess Session) error {
	ttl := time.Until(sess.ExpiresAt)
	if ttl <= 0 {
		ttl = time.Hour
	}
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("session:%s", sess.ID)
	if err := s.client.Set(s.ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	s.client.SAdd(s.ctx, "active_sessions", sess.ID)
	s.client.SAdd(s.ctx, fmt.Sprintf("user_sessions:%s", sess.UserID), sess.ID)
	return nil
}

func (s *Store) GetSession(id string) (Session, error) {
	data, err := s.client.Get(s.ctx, fmt.Sprintf("session:%s", id)).Result()
	if err == redis.Nil {
		return Session{}, fmt.Errorf("session %q not found", id)
	}
	if err != nil {
		return Session{}, err
	}
	var sess Session
	return sess, json.Unmarshal([]byte(data), &sess)
}

func (s *Store) UpdateSession(sess Session) error {
	key := fmt.Sprintf("session:%s", sess.ID)
	ttl, err := s.client.TTL(s.ctx, key).Result()
	if err != nil || ttl <= 0 {
		ttl = time.Minute
	}
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return s.client.Set(s.ctx, key, data, ttl).Err()
}

func (s *Store) ExtendSession(id string, extraMinutes int) error {
	key := fmt.Sprintf("session:%s", id)
	currentTTL, err := s.client.TTL(s.ctx, key).Result()
	if err != nil || currentTTL <= 0 {
		return fmt.Errorf("session %q not found or expired", id)
	}
	sess, err := s.GetSession(id)
	if err != nil {
		return err
	}
	extra := time.Duration(extraMinutes) * time.Minute
	sess.ExpiresAt = sess.ExpiresAt.Add(extra)
	data, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return s.client.Set(s.ctx, key, data, currentTTL+extra).Err()
}

func (s *Store) DeleteSession(id string) error {
	sess, _ := s.GetSession(id)
	s.client.Del(s.ctx, fmt.Sprintf("session:%s", id))
	s.client.Del(s.ctx, fmt.Sprintf("session:%s:desired", id))
	s.client.Del(s.ctx, fmt.Sprintf("session:%s:observed", id))
	s.client.SRem(s.ctx, "active_sessions", id)
	if sess.UserID != "" {
		s.client.SRem(s.ctx, fmt.Sprintf("user_sessions:%s", sess.UserID), id)
	}
	return nil
}

// ListActiveSessions returns sessions with status running or creating.
func (s *Store) ListActiveSessions() ([]Session, error) {
	ids, err := s.client.SMembers(s.ctx, "active_sessions").Result()
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(ids))
	for _, id := range ids {
		sess, err := s.GetSession(id)
		if err != nil {
			s.client.SRem(s.ctx, "active_sessions", id)
			continue
		}
		if sess.Status == "running" || sess.Status == "creating" {
			out = append(out, sess)
		}
	}
	return out, nil
}

// ListAllSessions returns all tracked sessions regardless of status.
func (s *Store) ListAllSessions() ([]Session, error) {
	ids, err := s.client.SMembers(s.ctx, "active_sessions").Result()
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(ids))
	for _, id := range ids {
		sess, err := s.GetSession(id)
		if err != nil {
			s.client.SRem(s.ctx, "active_sessions", id)
			continue
		}
		out = append(out, sess)
	}
	return out, nil
}

// CountUserActiveSessions returns the number of active sessions for a user.
func (s *Store) CountUserActiveSessions(userID string) (int, error) {
	ids, err := s.client.SMembers(s.ctx, fmt.Sprintf("user_sessions:%s", userID)).Result()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, id := range ids {
		sess, err := s.GetSession(id)
		if err != nil {
			s.client.SRem(s.ctx, fmt.Sprintf("user_sessions:%s", userID), id)
			continue
		}
		if sess.Status == "running" || sess.Status == "creating" {
			count++
		}
	}
	return count, nil
}

// ─── Reconcile versioning ─────────────────────────────────────────────────────

// TouchDesiredVersion increments the desired version for a session.
func (s *Store) TouchDesiredVersion(sessionID, reason string) (int64, error) {
	key := fmt.Sprintf("session:%s:desired", sessionID)
	v, err := s.client.Incr(s.ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("touch desired version: %w", err)
	}
	if ttl, _ := s.client.TTL(s.ctx, fmt.Sprintf("session:%s", sessionID)).Result(); ttl > 0 {
		s.client.Expire(s.ctx, key, ttl)
	}
	return v, nil
}

// GetDesiredVersion returns the current desired version counter.
func (s *Store) GetDesiredVersion(sessionID string) (int64, error) {
	v, err := s.client.Get(s.ctx, fmt.Sprintf("session:%s:desired", sessionID)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return v, err
}

// GetReconcileMeta returns the observed reconcile metadata.
func (s *Store) GetReconcileMeta(sessionID string) (ReconcileMeta, error) {
	data, err := s.client.Get(s.ctx, fmt.Sprintf("session:%s:observed", sessionID)).Result()
	if err == redis.Nil {
		desired, _ := s.GetDesiredVersion(sessionID)
		return ReconcileMeta{DesiredVersion: desired}, nil
	}
	if err != nil {
		return ReconcileMeta{}, err
	}
	var m ReconcileMeta
	return m, json.Unmarshal([]byte(data), &m)
}

// MarkReconcileSuccess records a successful reconcile.
func (s *Store) MarkReconcileSuccess(sessionID string, version int64, duration time.Duration, reason string) error {
	meta := ReconcileMeta{DesiredVersion: version, LastReconciled: time.Now().UTC(), Reason: reason}
	data, _ := json.Marshal(meta)
	key := fmt.Sprintf("session:%s:observed", sessionID)
	ttl, _ := s.client.TTL(s.ctx, fmt.Sprintf("session:%s", sessionID)).Result()
	if ttl <= 0 {
		ttl = time.Hour
	}
	s.client.Set(s.ctx, key, data, ttl)

	sess, err := s.GetSession(sessionID)
	if err != nil {
		return nil
	}
	sess.LastReconciledVersion = version
	sess.LastReconciledAt = meta.LastReconciled
	sess.LastReconciledDurationMs = duration.Milliseconds()
	sess.LastReconcileError = ""
	return s.UpdateSession(sess)
}

// MarkReconcileFailure records a failed reconcile.
func (s *Store) MarkReconcileFailure(sessionID, errMsg string) error {
	sess, err := s.GetSession(sessionID)
	if err != nil {
		return nil
	}
	sess.LastReconcileError = errMsg
	sess.LastReconciledAt = time.Now().UTC()
	return s.UpdateSession(sess)
}

// ─── Grant operations ─────────────────────────────────────────────────────────

func (s *Store) SaveGrant(g GrantRecord) error {
	data, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return s.client.Set(s.ctx, fmt.Sprintf("grant:pod:%s", g.PodIP), data, 0).Err()
}

func (s *Store) GetGrant(podIP string) (GrantRecord, error) {
	data, err := s.client.Get(s.ctx, fmt.Sprintf("grant:pod:%s", podIP)).Result()
	if err == redis.Nil {
		return GrantRecord{}, fmt.Errorf("grant for pod %q not found", podIP)
	}
	if err != nil {
		return GrantRecord{}, err
	}
	var g GrantRecord
	return g, json.Unmarshal([]byte(data), &g)
}

func (s *Store) DeleteGrant(podIP string) error {
	return s.client.Del(s.ctx, fmt.Sprintf("grant:pod:%s", podIP)).Err()
}
