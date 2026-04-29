# Nexus OSS — Architecture

## Overview

Nexus OSS is a **generic infrastructure layer** for orchestrating isolated, ephemeral challenge environments. It is decoupled from any specific CTF platform, scoring system, or billing logic.

```
┌─────────────────────────────────────────────────────────────┐
│                    Consumer (CTF Platform)                   │
│            POST /api/v1/sessions  { challenge_id, user_id } │
└───────────────────────────┬─────────────────────────────────┘
                            │ HTTP REST
┌───────────────────────────▼─────────────────────────────────┐
│                     nexus-engine (Go)                        │
│                                                              │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────────┐   │
│  │ Gin HTTP API│  │ Reconciliation│  │  k3s Client      │   │
│  │             │  │ Controller   │  │  (client-go)     │   │
│  │ /sessions   │  │ (worker pool)│  │                  │   │
│  │ /challenges │  │              │  │  SpawnPod()      │   │
│  │ /admin      │  │ 15s interval │  │  TerminatePod()  │   │
│  └──────┬──────┘  └──────┬───────┘  └──────────────────┘   │
│         │                │                                   │
│         │    ┌───────────┴────────┐                         │
│         │    │  Redis (state)      │                         │
│         │    │  session:<id>       │                         │
│         │    │  challenge:<id>     │                         │
│         │    │  active_sessions    │                         │
│         │    └────────────────────┘                         │
│         │ gRPC                                              │
└─────────┼───────────────────────────────────────────────────┘
          │
          │  gRPC (mTLS in prod / insecure in dev)
          │
┌─────────▼───────────────────────────────────────────────────┐
│                  nexus-node-agent (Rust)                     │
│                                                              │
│  ┌─────────────────┐  ┌────────────────┐  ┌─────────────┐  │
│  │ EnsureUserIso   │  │ GrantPodAccess │  │  WireGuard  │  │
│  │ RevokeUserIso   │  │ RevokePodAccess│  │  EnsurePeer │  │
│  └────────┬────────┘  └───────┬────────┘  └──────┬──────┘  │
│           │                   │                   │         │
│  ┌────────▼───────────────────▼───────────────────▼──────┐  │
│  │              Kernel Adapters                           │  │
│  │  ipset (hash:ip)  iptables (FORWARD)  wg syncconf    │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

---

## Control Plane: nexus-engine

**Language:** Go 1.22  
**Runtime:** Gin (HTTP), client-go (k3s), go-redis (state)

### Session lifecycle

```
POST /api/v1/sessions
       │
       ├─→ Validate: challenge exists, user_id present
       ├─→ Check: session limit (if configured)
       ├─→ k3s: SpawnPod() → waits for PodIP (90s timeout)
       ├─→ node agent: GrantPodAccess(user_id, pod_ip)  [prod]
       ├─→ node agent: EnsureUserIsolation(user_id, vpn_ip)  [prod]
       ├─→ Redis: SaveSession()
       ├─→ Redis: TouchDesiredVersion() → enqueue reconcile
       └─→ Return: session_id, pod_ip, expires_at
```

### Reconciliation controller

The controller runs as a background process. It ensures **desired state converges to actual state**:

1. **Bootstrap scan** — on startup, enqueues all active sessions
2. **Periodic scan** — every `NEXUS_RECONCILE_INTERVAL` (±20% jitter)
3. **Touch-triggered** — on session create/terminate/extend
4. **Worker pool** — `NEXUS_MAX_WORKERS` goroutines drain the job queue
5. **Idempotent repairs** — re-applies VPN grants (safe to duplicate), re-checks pod health
6. **TTL enforcement** — expires sessions past `ExpiresAt`
7. **Cleanup loop** — removes orphaned pods every 5 minutes

---

## Execution Plane: nexus-node-agent

**Language:** Rust (tokio, tonic)  
**Privileges:** Runs with `CAP_NET_ADMIN` for kernel operations

### Per-user VPN isolation (prod mode)

```
EnsureUserIsolation(user_id="alice", vpn_ip="10.8.0.2")

  1. ipset create nexus-user-alice hash:ip -exist
  2. iptables -I FORWARD 1 -s 10.8.0.2 -m set --match-set nexus-user-alice dst -j ACCEPT
  3. iptables -I FORWARD 2 -s 10.8.0.2 -j DROP
```

When a pod is granted:
```
GrantPodAccess(user_id="alice", pod_ip="10.244.0.5")

  1. ipset add nexus-user-alice 10.244.0.5 -exist
```

Result: Alice's VPN traffic (`10.8.0.2`) can only reach her pod IP (`10.244.0.5`). All other traffic is dropped at the FORWARD chain.

### Idempotency guarantees

| Operation | Idempotent | How |
|---|---|---|
| `EnsureUserIsolation` | ✅ | `ipset create -exist`, `iptables -C` before `-I` |
| `RevokeUserIsolation` | ✅ | loop `-D` until not found |
| `GrantPodAccess` | ✅ | `ipset add -exist` |
| `RevokePodAccess` | ✅ | `ipset del`, ignores "element not found" |
| `EnsureWireGuardPeer` | ✅ | Remove block then re-append, `wg syncconf` |
| `RevokeWireGuardPeer` | ✅ | Remove block, `wg set peer remove` |

---

## Operator Interface: nexus-cli

**Language:** Go 1.22  
**UI:** Cobra (commands) + Bubbletea (TUI) + Lipgloss (styling)

### TUI tabs

| Tab | Content |
|---|---|
| Sessions | Live session table with status, pod IP, TTL |
| Challenges | Registered challenges, ports, images |
| System | Session/pod counts, mode, registry |
| Controller | Worker stats, queue depth, reconcile interval |

Polling interval: 3 seconds. Keyboard: `←/→` tabs, `↑/↓` rows, `r` refresh, `q` quit.

---

## State Schema (Redis)

```
session:<id>           → Session JSON (TTL = session expiry)
session:<id>:desired   → int64 counter (desired reconcile version)
session:<id>:observed  → ReconcileMeta JSON
challenge:<id>         → Challenge JSON (no TTL)
active_sessions        → set of session IDs
user_sessions:<uid>    → set of session IDs for user
grant:pod:<pod_ip>     → GrantRecord JSON
challenges             → set of challenge IDs
```

---

## Operating Modes

| Feature | dev | prod |
|---|---|---|
| VPN isolation (ipset/iptables) | ❌ Optional | ✅ Required |
| `vpn_ip` in session create | Optional | Required |
| mTLS on gRPC | ❌ (insecure) | ✅ |
| Pod access | Direct pod IP | VPN only |
| Network policy | allow-all | VPN-subnet-only |
