# Nexus OSS API Reference

Base URL: `http://<engine-host>:<port>` (default: `http://localhost:8081`)

All request/response bodies are JSON.

---

## Health

### `GET /health`

```json
{ "status": "healthy", "service": "nexus-engine", "mode": "dev", "timestamp": "..." }
```

---

## Challenges

### `POST /api/v1/challenges` — Register a challenge

Builds the Docker image via `nerdctl build` and registers it.

**Request:**
```json
{
  "name": "pwn-101",
  "dockerfile_path": "./challenges/pwn-101/Dockerfile",
  "ttl_minutes": 60,
  "ports": [4444]
}
```

| Field | Required | Description |
|---|---|---|
| `name` | ✅ | Challenge name |
| `dockerfile_path` | ✅ | Path to Dockerfile on engine host |
| `ttl_minutes` | ❌ | Session TTL (default: env var) |
| `ports` | ❌ | Exposed TCP ports |

**Response 201:**
```json
{
  "id": "pwn-101-a1b2c3d4",
  "name": "pwn-101",
  "image": "localhost:5000/pwn-101:latest",
  "ttl_minutes": 60,
  "ports": [4444]
}
```

**Errors:** `400` validation | `422 BUILD_FAILED` nerdctl error

---

### `GET /api/v1/challenges` — List

### `GET /api/v1/challenges/:id` — Get

### `DELETE /api/v1/challenges/:id` — Delete (does not affect existing sessions)

### `POST /api/v1/challenges/:id/rebuild` — Rebuild image

---

## Sessions

### `POST /api/v1/sessions` — Create session

**Request:**
```json
{
  "challenge_id": "pwn-101-a1b2c3d4",
  "user_id": "alice",
  "vpn_ip": "10.8.0.5"
}
```

| Field | Required | Description |
|---|---|---|
| `challenge_id` | ✅ | Registered challenge ID |
| `user_id` | ✅ | Stable identifier for network isolation |
| `vpn_ip` | ✅ prod / ❌ dev | WireGuard VPN IP |

**Response 201:**
```json
{
  "session_id": "sess-a1b2c3d4",
  "user_id": "alice",
  "pod_ip": "10.244.0.5",
  "status": "running",
  "expires_at": "2025-01-01T01:00:00Z"
}
```

**Errors:**

| Code | Error | Cause |
|---|---|---|
| 400 | validation | Missing user_id / challenge_id / vpn_ip (prod) |
| 404 | `CHALLENGE_NOT_FOUND` | Unknown challenge |
| 409 | `SESSION_LIMIT_REACHED` | `NEXUS_MAX_SESSIONS_PER_USER` exceeded |
| 503 | `POD_SPAWN_FAILED` | k3s pod creation failed |

---

### `GET /api/v1/sessions` — List all

### `GET /api/v1/sessions/:id` — Get

### `DELETE /api/v1/sessions/:id` — Terminate (revokes VPN + deletes pod)

### `POST /api/v1/sessions/:id/extend` — Add time

```json
{ "duration_minutes": 30 }
```

---

## Admin

### `GET /api/v1/admin/cluster/health`

```json
{ "status": "healthy", "redis": "ok", "node_agent": "healthy", "mode": "prod" }
```

### `GET /api/v1/admin/config` — Running config

### `POST /api/v1/admin/reconcile` — Trigger immediate reconcile for all sessions

---

## Debug

### `GET /debug/system` — Overview (sessions, pods, mode, registry)

### `GET /debug/controller` — Reconciler stats

```json
{ "queued": 0, "in_flight": 2, "reconcile_interval": "15s", "workers": 5, "status": "running" }
```

### `GET /metrics` — Prometheus metrics

| Metric | Description |
|---|---|
| `nexus_reconcile_cycles_total` | Total reconcile cycles |
| `nexus_reconcile_repairs_total` | Repairs performed |
| `nexus_nodeagent_rpc_errors_total` | gRPC errors to node agent |
