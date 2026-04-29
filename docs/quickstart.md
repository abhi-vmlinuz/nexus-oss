# Quickstart — Nexus OSS in 10 Minutes

> **Goal:** Start nexus-engine + nexus-node-agent, register a challenge, spawn a session, verify with the TUI.

## Prerequisites

- Ubuntu 22.04+ or Debian 12+ (bare metal or VM)
- Root access
- 2 CPU / 2 GB RAM minimum
- Internet access (for k3s install + image pulls)

---

## 1. Clone & Build

```bash
git clone https://github.com/nexus-oss/nexus.git
cd nexus

# Build the Go engine
cd nexus-engine && make build && cd ..

# Build the Rust node agent
cd nexus-node-agent && cargo build --release && cd ..

# Build the CLI
cd nexus-cli && go build -o nexus . && cd ..
```

---

## 2. Bootstrap (one command)

```bash
sudo bash deploy/setup.sh
```

The script will interactively ask for:
- **Mode**: `dev` (local testing) or `prod` (strict VPN isolation)
- **Registry URL**: `localhost:5000` (default: deploys a local registry in k3s)
- **Redis URL**: `redis://localhost:6379` (default: uses system Redis)

> **Non-interactive (CI/CD):**
> ```bash
> NEXUS_MODE=dev sudo bash deploy/setup.sh --non-interactive
> ```

---

## 3. Verify the engine is running

```bash
nexus status
# ✅ Engine: healthy | mode=dev | time=2025-01-01T00:00:00Z
#    Sessions: 0  Pods: 0  Registry: localhost:5000
```

Or with curl:
```bash
curl http://localhost:8081/health
```

---

## 4. Register your first challenge

Create a `Dockerfile` for your challenge:
```dockerfile
# challenges/hello-pwn/Dockerfile
FROM ubuntu:22.04
RUN apt-get update && apt-get install -y netcat-openbsd
EXPOSE 4444
CMD ["nc", "-lk", "-p", "4444", "-e", "/bin/bash"]
```

Register it:
```bash
nexus challenge register \
  --name hello-pwn \
  --dockerfile ./challenges/hello-pwn/Dockerfile \
  --ports 4444
```

This calls `nerdctl build`, pushes to `localhost:5000`, and registers the image.

---

## 5. Spawn a session

```bash
nexus session create --challenge hello-pwn-<id> --user alice

# ✅ Session created
#    Session:   sess-a1b2c3d4
#    Pod IP:    10.244.0.5
#    Status:    running
#    Expires:   2025-01-01T01:00:00Z
```

In **dev mode**: connect directly to the pod IP.
In **prod mode**: connect only via WireGuard VPN (`vpn_ip` required).

---

## 6. Open the TUI dashboard

```bash
nexus tui
```

Navigate with `←/→` or `1-4` to switch tabs (Sessions / Challenges / System / Controller).
Press `r` to refresh, `q` to quit.

---

## 7. Run smoke tests

```bash
bash scripts/smoke_test.sh
```

Tests all API endpoints with `curl` and validates responses. Safe to run against a running engine.

---

## 8. Clean up

```bash
# Terminate a session
nexus session terminate sess-a1b2c3d4

# Delete a challenge
nexus challenge delete hello-pwn-<id>

# Stop services
sudo systemctl stop nexus-engine nexus-node-agent
```

---

## Environment Variables Reference

| Variable | Default | Description |
|---|---|---|
| `NEXUS_MODE` | `dev` | `dev` or `prod` |
| `NEXUS_PORT` | `8081` | Engine HTTP port |
| `NEXUS_REDIS_URL` | `redis://localhost:6379` | Redis connection |
| `NEXUS_REGISTRY_URL` | `localhost:5000` | Container registry |
| `NEXUS_NODE_AGENT_ADDR` | `localhost:50051` | Node agent gRPC |
| `NEXUS_NODE_AGENT_INSECURE` | `true` in dev | Disable mTLS |
| `NEXUS_DEFAULT_SESSION_TTL_MINUTES` | `60` | Default session lifetime |
| `NEXUS_MAX_SESSIONS_PER_USER` | `0` (unlimited) | Per-user session cap |
| `NEXUS_K3S_NAMESPACE` | `nexus-challenges` | Pod namespace |
| `NEXUS_RECONCILE_INTERVAL` | `15s` | Reconcile frequency |
| `NEXUS_MAX_WORKERS` | `5` | Reconcile worker pool |

---

## Troubleshooting

```bash
# Engine logs
journalctl -u nexus-engine -f

# Node agent logs
journalctl -u nexus-node-agent -f

# k3s pods
k3s kubectl get pods -n nexus-challenges

# Redis check
redis-cli ping

# Node agent gRPC health (via CLI)
nexus admin health
```

See [troubleshooting.md](troubleshooting.md) for common issues.
