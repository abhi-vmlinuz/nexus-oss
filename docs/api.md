# Nexus API Reference

This document provides a comprehensive guide to the HTTP APIs provided by **Nexus Engine**.

---

## 🛰️ Base URL
All API endpoints are prefixed with:
`http://<engine-host>:8081`

---

## 🧩 Session API (Public)
Used by frontends or participants to manage challenge instances.

### 🟢 Create Session
`POST /api/v1/sessions`

Spawns a new challenge instance (Pod) for a specific user.

**Request Body:**
```json
{
  "challenge_id": "test-challenge-abcd1234",
  "user_id": "player-1",
  "vpn_ip": "10.13.37.1"
}
```
*   `challenge_id` (Required): The registered ID of the challenge.
*   `user_id` (Required): Unique identifier for the user. Used for network isolation.
*   `vpn_ip` (Optional): The user's VPN IP. **Required in production mode** for firewall isolation.

**Response (201 Created):**
```json
{
  "session_id": "sess-a1b2c3d4",
  "user_id": "player-1",
  "challenge_id": "test-challenge-abcd1234",
  "pod_ip": "10.42.0.5",
  "status": "running",
  "created_at": "2026-05-01T17:30:00Z",
  "expires_at": "2026-05-01T18:30:00Z",
  "ports": [80, 8080],
  "services": [
    {"name": "web", "port": 80},
    {"name": "api", "port": 8080}
  ]
}
```

### 🟡 Get Session Status
`GET /api/v1/sessions/:id`

**Response (200 OK):**
Returns the same structure as the Create response.

### 🔴 Terminate Session
`DELETE /api/v1/sessions/:id`

Terminates the instance and revokes network access.

---

## 🛠️ Challenge API (Admin)
Used to register and manage challenge definitions.

### 🟢 Register Challenge
`POST /api/v1/challenges`

Registers a challenge by building its image from a Dockerfile or Compose file.

**Request Body (Single Container):**
```json
{
  "name": "pwn-challenge",
  "dockerfile_path": "/absolute/path/to/Dockerfile",
  "ttl_minutes": 60,
  "resources": {
    "cpu": "0.5",
    "memory": "256Mi"
  },
  "readiness_probe": {
    "http_get": { "path": "/", "port": 80 },
    "initial_delay_seconds": 5
  }
}
```

**Request Body (Multi-Container):**
```json
{
  "name": "complex-web",
  "compose_path": "/absolute/path/to/docker-compose.yml"
}
```

**Response (201 Created):**
```json
{
  "id": "pwn-challenge-1234abcd",
  "name": "pwn-challenge",
  "image": "localhost:5000/pwn-challenge:latest",
  "ports": [80],
  "ttl_minutes": 60
}
```

### 🔵 List Challenges
`GET /api/v1/challenges`

---

## 🛡️ Admin & Monitoring API
Restricted endpoints for cluster visibility and maintenance.

### 🩺 Cluster Health
`GET /api/v1/admin/cluster/health`

Returns health status of Engine, Redis, and Node Agent.

### ⚙️ Get Configuration
`GET /api/v1/admin/config`

Returns current runtime configuration including default resource limits.

### 📊 Cluster Visibility
*   `GET /api/v1/admin/nodes`: List K8s nodes and their status.
*   `GET /api/v1/admin/cluster/pods`: Raw list of all challenge pods.
*   `GET /api/v1/admin/registry/images`: List images stored in the local registry.

---

## 💓 Meta & Diagnostics
*   `/health`: Liveness check for the Engine process.
*   `/metrics`: Prometheus-compatible metrics endpoint.
*   `/debug/system`: High-level system statistics (Total sessions, total pods).
*   `/debug/controller`: Internal reconciler loop statistics.
