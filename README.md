# Nexus OSS

> **A self-hosted, bare-metal CTF challenge orchestration platform.**  
> Deploy, isolate, and manage ephemeral hacking challenges on your own infrastructure — no cloud required.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.21+-00ADD8.svg)](https://golang.org)
[![Rust Edition](https://img.shields.io/badge/rust-2021-orange.svg)](https://www.rust-lang.org)

---

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Components](#components)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Configuration](#configuration)
- [CLI Reference](#cli-reference)
- [API Reference](#api-reference)
- [Uninstalling](#uninstalling)
- [Project Structure](#project-structure)
- [Development](#development)

---

## Overview

Nexus OSS is a control plane for running containerized CTF (Capture The Flag) challenges on bare-metal or self-hosted virtual machines. It replaces expensive cloud-based orchestration with a lightweight K3s + nerdctl stack, providing:

- **Multi-tenant isolation** via WireGuard VPN and `ipset`/`iptables` network policies
- **Ephemeral challenge instances** — each player session gets its own isolated container
- **Operator CLI** with a live TUI dashboard for monitoring sessions in real time
- **Plug-in registries** — use a local registry or authenticate with Docker Hub / GHCR
- **`dev` and `prod` modes** for local testing and hardened production deployments

---

## Architecture

```
                 ┌─────────────────────────────────────────┐
                 │              Operator (you)             │
                 │           nexus tui / nexus CLI         │
                 └────────────────┬────────────────────────┘
                                  │ HTTP REST
                 ┌────────────────▼─────────────────────────┐
                 │            nexus-engine                  │
                 │  Gin · Redis State · K3s Controller      │
                 └───────┬──────────────┬───────────────────┘
                         │ gRPC         │ Kubernetes API
          ┌──────────────▼────┐    ┌─────▼─────────────────┐
          │  nexus-node-agent │    │         k3s           │
          │  (Rust, privd)    │    │  nexus-challenges NS  │
          │  ipset · WireGuard│    │  Challenge Pods/Svcs  │
          └───────────────────┘    └───────────────────────┘
                         │
          ┌──────────────▼───────────┐
          │   Container Registry     │
          │  local:5000 / GHCR / Hub │
          └──────────────────────────┘
```

---

## Components

| Component | Language | Role |
|---|---|---|
| **nexus-engine** | Go (Gin) | Central REST API, session lifecycle, K3s reconciliation |
| **nexus-cli** | Go (Cobra + Bubbletea) | Operator CLI and live TUI dashboard |
| **nexus-node-agent** | Rust (Tonic/gRPC) | Privileged daemon — manages `ipset`, `iptables`, WireGuard |
| **nexus-installer** | Go (Bubbletea TUI) | Interactive full-screen setup wizard |

### nexus-engine
The core API server. It manages the full challenge session lifecycle:
- Creates/destroys K3s pods for each player session
- Tracks session state in Redis (TTL-based cleanup)
- Communicates with `nexus-node-agent` via gRPC for network configuration
- Exposes a REST API consumed by the CLI and external platforms (e.g., CTFd)

### nexus-cli
The operator control surface. Provides:
- `nexus tui` — a live, updating terminal dashboard for session monitoring
- `nexus challenge` — create, list, and delete challenge definitions
- `nexus session` — inspect and terminate player sessions
- `nexus status` — health check against the engine
- `nexus config` — view and validate the local configuration

### nexus-node-agent
A privileged Rust daemon that runs alongside the engine on each node. It handles all kernel-level network operations that require `CAP_NET_ADMIN`:
- WireGuard peer management for VPN-based isolation
- `ipset`/`iptables` rule enforcement per session
- Accepts commands from `nexus-engine` via an insecure gRPC socket (local only)

### nexus-installer
A full-screen TUI installer written in Go using Bubbletea. Replaces the legacy `deploy/setup.sh` with an interactive, distro-aware setup experience that handles the complete 9-phase bootstrap automatically.

---

## Prerequisites

| Dependency | Minimum Version | Notes |
|---|---|---|
| Linux x86_64 | Kernel 5.6+ | WireGuard requires 5.6+; CachyOS/Fedora kernels work great |
| Go | 1.21+ | For building `nexus-engine` and `nexus-cli` |
| Rust + Cargo | 1.70+ | For building `nexus-node-agent` |
| `git` | Any | To clone the repository |
| `curl` | Any | Used by the K3s installer |
| `sudo` | Any | Required for system-level operations |

> **SELinux Note (Fedora/RHEL):** The installer automatically runs `restorecon` on all installed binaries. No manual SELinux configuration is needed.

---

## Installation

### One-Command Bootstrap (Recommended)

Install Nexus OSS on any supported Linux distribution with a single command:

```bash
curl -fSL https://raw.githubusercontent.com/abhi-vmlinuz/nexus-oss/main/bootstrap.sh | bash
```

### Manual Installation

If you prefer to audit the code first or clone manually:

```bash
git clone https://github.com/abhi-vmlinuz/nexus-oss.git
cd nexus-oss
chmod +x build-installer.sh
./build-installer.sh
```

### What the Installer Does (9 Phases)

| Phase | Description |
|---|---|
| 0 | Validate `sudo` credentials, initialize install log at `/var/log/nexus-install.log` |
| 1 | Detect distro, install system packages (`curl`, `jq`, `wireguard`, `golang`, `rust`, etc.) |
| 2 | Install K3s in standalone mode (Traefik disabled), create `nexus-challenges` namespace |
| 3 | Install `nerdctl` + containerd, configure the `nexus` group and socket permissions |
| 4 | Set up the container registry (local on `:5000` or authenticate with GHCR/Docker Hub) |
| 5 | Deploy Redis (Docker container or host service) |
| 6 | Configure WireGuard VPN server on `wg0` (`prod` mode only) |
| 7 | Build and install `nexus-engine`, `nexus` (CLI), and `nexus-node-agent` from source |
| 8 | Write configuration to `~/.config/nexus/config.json` |
| 9 | Generate and enable systemd unit files, start all services |

### Supported Distributions

| Distro Family | Package Manager | Status |
|---|---|---|
| Debian / Ubuntu | `apt` | ✅ Supported |
| Fedora / RHEL / CentOS | `dnf` / `yum` | ✅ Supported (SELinux-aware) |
| Arch Linux / Manjaro | `pacman` | ✅ Supported |
| openSUSE | `zypper` | ✅ Supported |

---

## Configuration

After installation, the configuration is stored at `~/.config/nexus/config.json` (mode `0600`).

```json
{
  "engine": {
    "url": "http://localhost:8081",
    "mode": "dev"
  },
  "registry": {
    "type": "local",
    "url": "localhost:5000",
    "auth": {
      "type": "none",
      "username": "",
      "password": ""
    }
  },
  "redis": {
    "url": "redis://localhost:6379"
  },
  "node_agent": {
    "addr": "localhost:50051"
  },
  "k8s": {
    "namespace": "nexus-challenges"
  }
}
```

### Environment Variable Overrides

All configuration values can be overridden with environment variables:

| Variable | Default | Description |
|---|---|---|
| `NEXUS_MODE` | `dev` | `dev` or `prod` |
| `NEXUS_PORT` | `8081` | Engine listen port |
| `NEXUS_REDIS_URL` | `redis://localhost:6379` | Redis connection string |
| `NEXUS_REGISTRY_URL` | `localhost:5000` | Container registry URL |
| `NEXUS_NODE_AGENT_ADDR` | `localhost:50051` | Node agent gRPC address |
| `NEXUS_K3S_NAMESPACE` | `nexus-challenges` | Kubernetes namespace |
| `NEXUS_ENGINE_URL` | `http://localhost:8081` | Used by the CLI to reach the engine |
| `KUBECONFIG` | `/etc/rancher/k3s/k3s.yaml` | K3s kubeconfig path |

### Modes

**`dev` mode** (default):
- WireGuard VPN is **disabled**
- Services run without strict network isolation
- Ideal for local development and testing

**`prod` mode**:
- WireGuard VPN is enabled on `wg0` (`10.8.0.1/24`, port `51820`)
- `ipset`/`iptables` rules are enforced per session
- Systemd services run with `CAP_NET_ADMIN` capabilities

---

## CLI Reference

```
nexus [command]

Available Commands:
  challenge   Manage CTF challenge definitions
  session     Inspect and manage player sessions
  compose     Deploy multi-container challenge stacks
  config      Show or update Nexus configuration
  status      Health check against the engine
  tui         Open the live TUI dashboard

Global Flags:
  --engine string   Override the Nexus engine URL
```

### Examples

```bash
# Check engine and redis health
nexus status

# Open the live operator dashboard
nexus tui

# List all deployed challenges
nexus challenge list

# Create a new challenge from a docker-compose file
nexus compose up ./testing/pwn-101/docker-compose.yml

# View current configuration
nexus config view

# Validate connectivity
nexus config validate
```

### Shell Completion

Nexus CLI supports tab-completion for Bash, Zsh, Fish, and PowerShell. To enable it for your current session:

```bash
# For Bash
source <(nexus completion bash)

# For Zsh
source <(nexus completion zsh)

# For Fish
nexus completion fish | source
```

To make it permanent, add the appropriate command to your shell's configuration file (e.g., `~/.bashrc` or `~/.zshrc`).

---

## API Reference

Nexus OSS provides a complete REST API for session lifecycle management and challenge orchestration. This allows for easy integration with existing CTF platforms like CTFd.

For a full list of endpoints, request models, and response structures, see the [API Documentation](docs/api.md).

### Quick Example (Create Session)
```bash
curl -X POST http://localhost:8081/api/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "challenge_id": "pwn-101-abcd",
    "user_id": "player-1"
  }'
```

---

## Uninstalling

Run the uninstaller script to cleanly remove all Nexus components:

```bash
sudo ./deploy/uninstaller.sh
```

The uninstaller removes:
- **Systemd services**: `nexus-engine`, `nexus-node-agent`, `nexus-socket-fix`
- **Binaries**: `/usr/local/bin/nexus`, `/usr/local/bin/nexus-engine`, `/usr/local/bin/nexus-node-agent`
- **Configuration**: `~/.config/nexus/`
- **Containers**: `nexus-redis`, `nexus-registry` (via nerdctl)
- **WireGuard**: `wg0` interface and `/etc/wireguard/wg0.conf`

> **K3s and the `nexus-challenges` namespace are intentionally preserved** to protect any challenge data. The uninstaller will prompt you before removing them.

---

## Project Structure

```
nexus-oss/
├── build-installer.sh          # Bootstrap: build, run, and cleanup the installer
├── deploy/
│   ├── setup.sh                # Legacy bash installer (superseded by TUI)
│   ├── uninstaller.sh          # Full system cleanup script
│   ├── network-policy-dev.yaml # K3s network policy (dev)
│   └── network-policy-prod.yaml# K3s network policy (prod)
├── nexus-engine/               # Go — Core REST API server
│   ├── cmd/main.go             # Entry point
│   └── internal/
│       ├── api/                # Gin HTTP handlers
│       ├── controller/         # Session reconciliation loop
│       ├── k8s/                # K3s/Kubernetes client
│       ├── nodeagent/          # gRPC client for nexus-node-agent
│       ├── registry/           # Container registry interaction
│       ├── state/              # Redis state management
│       └── config/             # Environment-based configuration loader
├── nexus-cli/                  # Go — Operator CLI (Cobra + Bubbletea)
│   ├── main.go
│   ├── cmd/                    # Cobra subcommands
│   ├── client/                 # HTTP client for nexus-engine
│   ├── tui/                    # Live Bubbletea TUI dashboard
│   └── config/                 # Config loader with env fallback
├── nexus-node-agent/           # Rust — Privileged network daemon (Tonic gRPC)
│   ├── src/
│   │   ├── main.rs
│   │   ├── server.rs           # gRPC server implementation
│   │   ├── config.rs           # Config from environment
│   │   └── adapters/           # ipset, iptables, WireGuard adapters
│   └── Cargo.toml
├── nexus-installer/            # Go — Interactive TUI installer (Bubbletea)
│   ├── main.go                 # Bubbletea program entry + Update loop
│   ├── model.go                # Shared application state model
│   ├── pages.go                # TUI page renderers
│   ├── styles.go               # Lipgloss design system
│   └── internal/
│       ├── installer.go        # 9-phase installation logic
│       └── exec.go             # Shell command runner with logging
├── docs/
│   ├── architecture.md
│   ├── api.md
│   └── quickstart.md
├── scripts/
│   └── smoke_test.sh           # Post-install smoke test
└── testing/
    ├── pwn-101/                # Example single-container challenge
    └── multi-pwn/              # Example multi-container challenge stack
```

---

## Development

### Building Individual Components

```bash
# Build nexus-engine
cd nexus-engine && go build -o nexus-engine ./cmd

# Build nexus-cli
cd nexus-cli && go build -o nexus .

# Build nexus-node-agent (Rust)
cd nexus-node-agent && cargo build --release

# Build nexus-installer TUI
cd nexus-installer && go build -o nexus-installer *.go
```

### Running in Dev Mode

```bash
# 1. Start Redis
nerdctl run -d --name nexus-redis -p 6379:6379 redis:7-alpine

# 2. Start the engine (dev mode uses defaults from config.json)
nexus-engine

# 3. In another terminal, verify
nexus status
nexus tui
```

### Smoke Test

After a fresh installation, run the bundled smoke test to verify all services are reachable:

```bash
chmod +x scripts/smoke_test.sh
./scripts/smoke_test.sh
```

### Install Logs

All installer output is permanently recorded at:

```
/var/log/nexus-install.log
```

---

## 🛠️ Troubleshooting

Facing issues with service permissions, SELinux, or loopback connectivity? 
Check our [Debugging Guide](DEBUGGING.md) for solutions to common setup hurdles.

## License

Apache 2.0 — see [LICENSE](LICENSE) for details.
