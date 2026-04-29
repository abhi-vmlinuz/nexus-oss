package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexus-oss/nexus/nexus-engine/internal/api"
)

// newTestRouter builds a gin router wired to a mock Deps.
// The mock store and k8s adapter are passed by the caller.
func newTestRouter(t *testing.T, d api.Deps) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api.Register(r, d)
	return r
}

// TestHealthEndpoint verifies GET /health returns 200 and "healthy".
func TestHealthEndpoint(t *testing.T) {
	d := minimalDeps(t)
	r := newTestRouter(t, d)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "healthy", body["status"])
}

// TestCreateSessionMissingUserID verifies that user_id is enforced.
func TestCreateSessionMissingUserID(t *testing.T) {
	d := minimalDeps(t)
	r := newTestRouter(t, d)

	payload := map[string]any{
		"challenge_id": "pwn-101",
		// user_id intentionally missing
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp, "error")
}

// TestCreateSessionMissingChallengeID verifies that challenge_id is enforced.
func TestCreateSessionMissingChallengeID(t *testing.T) {
	d := minimalDeps(t)
	r := newTestRouter(t, d)

	payload := map[string]any{
		"user_id": "alice",
		// challenge_id intentionally missing
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestGetSessionNotFound verifies 404 for unknown session IDs.
func TestGetSessionNotFound(t *testing.T) {
	d := minimalDeps(t)
	r := newTestRouter(t, d)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/does-not-exist", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGetChallengeNotFound verifies 404 for unknown challenge IDs.
func TestGetChallengeNotFound(t *testing.T) {
	d := minimalDeps(t)
	r := newTestRouter(t, d)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/challenges/no-such-challenge", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestListSessionsEmpty verifies that an empty session list returns 200.
func TestListSessionsEmpty(t *testing.T) {
	d := minimalDeps(t)
	r := newTestRouter(t, d)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, float64(0), body["count"])
}

// TestTerminateSessionNotFound verifies 404 on terminating a nonexistent session.
func TestTerminateSessionNotFound(t *testing.T) {
	d := minimalDeps(t)
	r := newTestRouter(t, d)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/ghost-session", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
