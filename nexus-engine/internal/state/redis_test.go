package state_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
)

// newTestStore creates a Store connected to a real Redis.
// Requires NEXUS_REDIS_URL env var or falls back to redis://localhost:6379.
// Tests are skipped if Redis is unavailable.
func newTestStore(t *testing.T) *state.Store {
	t.Helper()
	url := "redis://localhost:6379"
	s, err := state.New(url)
	if err != nil {
		t.Skipf("Redis unavailable (%v) — skipping integration test", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSaveAndGetChallenge(t *testing.T) {
	s := newTestStore(t)
	ch := state.Challenge{
		ID:             "test-ch-001",
		Name:           "Test Challenge",
		Image:          "localhost:5000/test-ch-001:latest",
		DockerfilePath: "./challenges/test-001/Dockerfile",
		TTLMinutes:     60,
		Ports:          []int{8080},
		Tag:            "latest",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	require.NoError(t, s.SaveChallenge(ch))

	got, err := s.GetChallenge(ch.ID)
	require.NoError(t, err)
	assert.Equal(t, ch.ID, got.ID)
	assert.Equal(t, ch.Name, got.Name)
	assert.Equal(t, ch.Image, got.Image)
	assert.Equal(t, ch.Ports, got.Ports)

	// Cleanup.
	s.DeleteChallenge(ch.ID)
}

func TestGetChallengeNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetChallenge("does-not-exist")
	assert.Error(t, err)
}

func TestSaveAndGetSession(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	sess := state.Session{
		ID:          "sess-test-001",
		UserID:      "alice",
		ChallengeID: "pwn-101",
		PodName:     "chal-sess-test-001",
		PodIP:       "10.244.0.5",
		Status:      "running",
		CreatedAt:   now,
		ExpiresAt:   now.Add(60 * time.Minute),
	}

	require.NoError(t, s.SaveSession(sess))

	got, err := s.GetSession(sess.ID)
	require.NoError(t, err)
	assert.Equal(t, sess.ID, got.ID)
	assert.Equal(t, sess.UserID, got.UserID)
	assert.Equal(t, sess.PodIP, got.PodIP)
	assert.Equal(t, "running", got.Status)

	// Cleanup.
	s.DeleteSession(sess.ID)
}

func TestSessionListFiltersExpired(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	active := state.Session{
		ID:          "sess-active-001",
		UserID:      "user-a",
		ChallengeID: "pwn-101",
		Status:      "running",
		CreatedAt:   now,
		ExpiresAt:   now.Add(60 * time.Minute),
	}
	require.NoError(t, s.SaveSession(active))
	defer s.DeleteSession(active.ID)

	list, err := s.ListActiveSessions()
	require.NoError(t, err)

	found := false
	for _, sess := range list {
		if sess.ID == active.ID {
			found = true
			break
		}
	}
	assert.True(t, found, "active session should appear in ListActiveSessions")
}

func TestCountUserActiveSessions(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	userID := "test-count-user"

	sessions := []state.Session{
		{ID: "sess-cu-001", UserID: userID, ChallengeID: "c1", Status: "running",
			CreatedAt: now, ExpiresAt: now.Add(60 * time.Minute)},
		{ID: "sess-cu-002", UserID: userID, ChallengeID: "c2", Status: "running",
			CreatedAt: now, ExpiresAt: now.Add(60 * time.Minute)},
	}
	for _, sess := range sessions {
		require.NoError(t, s.SaveSession(sess))
		defer s.DeleteSession(sess.ID)
	}

	count, err := s.CountUserActiveSessions(userID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 2, "user should have at least 2 active sessions")
}

func TestExtendSession(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	sess := state.Session{
		ID:          "sess-extend-001",
		UserID:      "bob",
		ChallengeID: "test",
		Status:      "running",
		CreatedAt:   now,
		ExpiresAt:   now.Add(60 * time.Minute),
	}
	require.NoError(t, s.SaveSession(sess))
	defer s.DeleteSession(sess.ID)

	require.NoError(t, s.ExtendSession(sess.ID, 30))

	updated, err := s.GetSession(sess.ID)
	require.NoError(t, err)
	expected := sess.ExpiresAt.Add(30 * time.Minute)
	// Allow 2s of test timing slack.
	assert.WithinDuration(t, expected, updated.ExpiresAt, 2*time.Second)
}

func TestReconcileVersioning(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	sess := state.Session{
		ID:          "sess-reconcile-001",
		UserID:      "charlie",
		ChallengeID: "test",
		Status:      "running",
		CreatedAt:   now,
		ExpiresAt:   now.Add(60 * time.Minute),
	}
	require.NoError(t, s.SaveSession(sess))
	defer s.DeleteSession(sess.ID)

	v1, err := s.TouchDesiredVersion(sess.ID, "create")
	require.NoError(t, err)
	assert.Equal(t, int64(1), v1)

	v2, err := s.TouchDesiredVersion(sess.ID, "extend")
	require.NoError(t, err)
	assert.Equal(t, int64(2), v2)

	require.NoError(t, s.MarkReconcileSuccess(sess.ID, v2, 10*time.Millisecond, "test"))

	meta, err := s.GetReconcileMeta(sess.ID)
	require.NoError(t, err)
	assert.Equal(t, v2, meta.DesiredVersion)
}
