#!/usr/bin/env bash
# deploy/setup.sh — Nexus OSS Bootstrap Script
#
# Installs and configures the full Nexus OSS stack on a Linux host.
# Supports: Ubuntu, Debian, Fedora, RHEL/CentOS (8+), Arch Linux, openSUSE
#
# Usage (interactive):
#   sudo bash setup.sh
#
# Usage (non-interactive / CI):
#   NEXUS_MODE=dev NEXUS_REGISTRY_URL=localhost:5000 bash setup.sh --non-interactive

set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

banner() {
echo -e "\033[1;36m
███╗   ██╗███████╗██╗  ██╗██╗   ██╗███████╗
████╗  ██║██╔════╝╚██╗██╔╝██║   ██║██╔════╝
██╔██╗ ██║█████╗   ╚███╔╝ ██║   ██║███████╗
██║╚██╗██║██╔══╝   ██╔██╗ ██║   ██║╚════██║
██║ ╚████║███████╗██╔╝ ██╗╚██████╔╝███████║
╚═╝  ╚═══╝╚══════╝╚═╝  ╚═╝ ╚═════╝ ╚══════╝
\033[0m"

echo -e "\033[0;37m  Bootstrapping Nexus OSS...\033[0m"
echo -e "\033[0;90m  Initializing infrastructure and runtime components\033[0m\n"
}

info()   { echo -e "${GREEN}→${NC} $*"; }
warn()   { echo -e "${YELLOW}⚠  $*${NC}"; }
die()    { echo -e "${RED}✗  $*${NC}" >&2; exit 1; }
ok()     { echo -e "${GREEN}✅ $*${NC}"; }

[[ $EUID -eq 0 ]] || die "Run as root: sudo bash setup.sh"

OS_ID=$(grep -E "^ID=" /etc/os-release | cut -d= -f2 | tr -d '"' | tr '[:upper:]' '[:lower:]')
OS_ID_LIKE=$(grep -E "^ID_LIKE=" /etc/os-release 2>/dev/null | cut -d= -f2 | tr -d '"' | tr '[:upper:]' '[:lower:]' || echo "")
ARCH=$(uname -m)

# ─── Package manager detection ────────────────────────────────────────────────
# Detect the package manager family and set PKG_MGR, then define pkg_install().
# Package names differ across distros — we map them explicitly.

detect_pkg_family() {
  # Returns: apt | dnf | yum | pacman | zypper | unknown
  if command -v apt-get &>/dev/null; then echo "apt"
  elif command -v dnf    &>/dev/null; then echo "dnf"
  elif command -v yum    &>/dev/null; then echo "yum"
  elif command -v pacman &>/dev/null; then echo "pacman"
  elif command -v zypper &>/dev/null; then echo "zypper"
  else echo "unknown"; fi
}

PKG_MGR=$(detect_pkg_family)
info "Detected OS: $OS_ID  ARCH: $ARCH  Package manager: $PKG_MGR"

# Map a logical package name to the distro-specific package name.
# Usage: resolve_pkg <logical-name>  →  prints the real package name
resolve_pkg() {
  local pkg="$1"
  case "$PKG_MGR" in
    apt)
      case "$pkg" in
        redis)       echo "redis-server" ;;
        wireguard)   echo "wireguard wireguard-tools" ;;
        iptables)    echo "iptables" ;;
        ipset)       echo "ipset" ;;
        ca-certs)    echo "ca-certificates" ;;
        kernel-dev)  echo "linux-headers-$(uname -r)" ;;
        *)           echo "$pkg" ;;
      esac ;;
    dnf|yum)
      case "$pkg" in
        redis)       echo "redis" ;;
        wireguard)   echo "wireguard-tools" ;;
        iptables)    echo "iptables" ;;
        ipset)       echo "ipset" ;;
        ca-certs)    echo "ca-certificates" ;;
        kernel-dev)  echo "kernel-devel-$(uname -r)" ;;
        wget)        echo "wget" ;;
        *)           echo "$pkg" ;;
      esac ;;
    pacman)
      case "$pkg" in
        redis)       echo "redis" ;;
        wireguard)   echo "wireguard-tools" ;;
        iptables)    echo "iptables" ;;
        ipset)       echo "ipset" ;;
        ca-certs)    echo "ca-certificates" ;;
        kernel-dev)  echo "linux-headers" ;;
        *)           echo "$pkg" ;;
      esac ;;
    zypper)
      case "$pkg" in
        redis)       echo "redis" ;;
        wireguard)   echo "wireguard-tools" ;;
        iptables)    echo "iptables" ;;
        ipset)       echo "ipset" ;;
        ca-certs)    echo "ca-certificates" ;;
        kernel-dev)  echo "kernel-devel" ;;
        *)           echo "$pkg" ;;
      esac ;;
    *) echo "$pkg" ;;
  esac
}

# Install one or more logical package names.
pkg_install() {
  local pkgs=()
  for logical in "$@"; do
    # resolve_pkg may return multiple words (e.g. "wireguard wireguard-tools")
    # so we use read to split them
    while IFS= read -r p; do
      pkgs+=("$p")
    done < <(resolve_pkg "$logical" | tr ' ' '\n')
  done

  info "Installing: ${pkgs[*]}"
  case "$PKG_MGR" in
    apt)    apt-get install -y -qq "${pkgs[@]}" ;;
    dnf)    dnf install -y -q "${pkgs[@]}" ;;
    yum)    yum install -y -q "${pkgs[@]}" ;;
    pacman) pacman -S --noconfirm --needed "${pkgs[@]}" ;;
    zypper) zypper install -y --no-recommends "${pkgs[@]}" ;;
    *)      die "Unsupported package manager. Install manually: ${pkgs[*]}" ;;
  esac
}

# Refresh the package index (distro-specific).
pkg_update() {
  case "$PKG_MGR" in
    apt)    apt-get update -qq ;;
    dnf)    dnf makecache -q ;;
    yum)    yum makecache -q ;;
    pacman) pacman -Sy --noconfirm ;;
    zypper) zypper refresh -q ;;
    *)      warn "Cannot refresh package index — unknown package manager" ;;
  esac
}

# Enable a service via systemd (works on all distros using systemd).
svc_enable_start() {
  local name="$1"
  # On Fedora/RHEL redis is just 'redis', on Debian it's 'redis-server'.
  # Try the provided name; if it doesn't exist try the alternate.
  if systemctl list-unit-files "${name}.service" &>/dev/null; then
    systemctl enable "$name" --quiet && systemctl start "$name"
  else
    warn "Service $name not found — skipping (check your installation)"
  fi
}

NON_INTERACTIVE=false
for arg in "$@"; do [[ "$arg" == "--non-interactive" ]] && NON_INTERACTIVE=true; done

prompt() {
  local var="$1" prompt="$2" default="$3"
  if [[ -n "${!var:-}" ]]; then info "$var = ${!var}"; return; fi
  if $NON_INTERACTIVE; then eval "$var='$default'"; info "$var = $default (default)"; return; fi
  read -rp "$(echo -e "${BOLD}${prompt}${NC} [${default}]: ")" val
  eval "$var='${val:-$default}'"
}

# ─── Configuration ─────────────────────────────────────────────────────────────
banner "Nexus OSS Bootstrap"

prompt NEXUS_MODE            "Mode (dev/prod)"         "dev"
prompt NEXUS_PORT            "Engine HTTP port"         "8081"
prompt NEXUS_NODE_AGENT_ADDR "Node agent gRPC address"  "localhost:50051"
prompt NEXUS_K3S_NAMESPACE   "Kubernetes namespace"     "nexus-challenges"

# -- Redis backend ----------------------------------------
echo ""
echo -e "${BOLD}Redis backend:${NC}"
echo "  1) Docker container  (recommended - isolated, portable, no host service)"
echo "  2) Host service      (system redis installed directly on the OS)"
echo ""

if [[ -n "${NEXUS_REDIS_BACKEND:-}" ]]; then
  info "NEXUS_REDIS_BACKEND already set: $NEXUS_REDIS_BACKEND"
elif $NON_INTERACTIVE; then
  NEXUS_REDIS_BACKEND="docker"
  info "NEXUS_REDIS_BACKEND = docker (non-interactive default)"
else
  read -rp "$(echo -e "${BOLD}Choose Redis backend${NC} [1/2, default 1]: ")" redis_choice
  case "${redis_choice:-1}" in
    2) NEXUS_REDIS_BACKEND="host" ;;
    *) NEXUS_REDIS_BACKEND="docker" ;;
  esac
fi

if [[ "$NEXUS_REDIS_BACKEND" == "docker" ]]; then
  NEXUS_REDIS_URL="redis://localhost:6379"
  ok "Redis -> Docker container on port 6379"
else
  prompt NEXUS_REDIS_URL "Redis URL" "redis://localhost:6379"
  ok "Redis -> host service ($NEXUS_REDIS_URL)"
fi

# -- Container registry -----------------------------------
echo ""
echo -e "${BOLD}Container registry:${NC}"
echo "  1) Local   (nerdctl container on this host - default, no auth)"
echo "  2) Docker Hub   (docker.io - requires username + token)"
echo "  3) GitHub Container Registry   (ghcr.io - requires PAT)"
echo "  4) AWS ECR   (requires aws-cli + IAM role)"
echo "  5) Custom   (any registry URL + optional credentials)"
echo ""

if [[ -n "${NEXUS_REGISTRY_TYPE:-}" ]]; then
  info "NEXUS_REGISTRY_TYPE already set: $NEXUS_REGISTRY_TYPE"
elif $NON_INTERACTIVE; then
  NEXUS_REGISTRY_TYPE="local"
  info "NEXUS_REGISTRY_TYPE = local (non-interactive default)"
else
  read -rp "$(echo -e "${BOLD}Choose registry${NC} [1-5, default 1]: ")" reg_choice
  case "${reg_choice:-1}" in
    2) NEXUS_REGISTRY_TYPE="dockerhub" ;;
    3) NEXUS_REGISTRY_TYPE="ghcr" ;;
    4) NEXUS_REGISTRY_TYPE="ecr" ;;
    5) NEXUS_REGISTRY_TYPE="custom" ;;
    *) NEXUS_REGISTRY_TYPE="local" ;;
  esac
fi

case "$NEXUS_REGISTRY_TYPE" in
  local)
    prompt NEXUS_REGISTRY_PORT "Local registry port" "5000"
    NEXUS_REGISTRY_URL="localhost:${NEXUS_REGISTRY_PORT:-5000}"
    ok "Registry -> local nerdctl container on port ${NEXUS_REGISTRY_PORT:-5000}" ;;
  dockerhub)
    prompt NEXUS_REGISTRY_USER "Docker Hub username" ""
    prompt NEXUS_REGISTRY_PASS "Docker Hub password/token" ""
    NEXUS_REGISTRY_URL="docker.io/${NEXUS_REGISTRY_USER}"
    ok "Registry -> Docker Hub ($NEXUS_REGISTRY_URL)" ;;
  ghcr)
    prompt NEXUS_REGISTRY_USER "GitHub username / org" ""
    prompt NEXUS_REGISTRY_PASS "GitHub PAT (write:packages scope)" ""
    NEXUS_REGISTRY_URL="ghcr.io/${NEXUS_REGISTRY_USER}"
    ok "Registry -> GHCR ($NEXUS_REGISTRY_URL)" ;;
  ecr)
    prompt NEXUS_ECR_REGION  "AWS region"     "us-east-1"
    prompt NEXUS_ECR_ACCOUNT "AWS account ID" ""
    NEXUS_REGISTRY_URL="${NEXUS_ECR_ACCOUNT}.dkr.ecr.${NEXUS_ECR_REGION}.amazonaws.com"
    ok "Registry -> AWS ECR ($NEXUS_REGISTRY_URL)" ;;
  custom)
    prompt NEXUS_REGISTRY_URL  "Registry URL (host:port)" ""
    prompt NEXUS_REGISTRY_USER "Username (blank = none)" ""
    prompt NEXUS_REGISTRY_PASS "Password (blank = none)" ""
    ok "Registry -> custom ($NEXUS_REGISTRY_URL)" ;;
esac

echo ""
info "Summary:"
echo "  mode=$NEXUS_MODE  port=$NEXUS_PORT"
echo "  redis=$NEXUS_REDIS_BACKEND ($NEXUS_REDIS_URL)"
echo "  registry=$NEXUS_REGISTRY_TYPE ($NEXUS_REGISTRY_URL)"
echo ""

if ! $NON_INTERACTIVE; then
  read -rp "$(echo -e "${BOLD}Proceed with installation? [y/N]: ${NC}")" confirm
  [[ "${confirm,,}" == "y" ]] || { echo "Aborted."; exit 0; }
fi

banner "Phase X: Build Nexus Components"

# Build nexus-engine (Go)
if [[ ! -f "$REPO_ROOT/nexus-engine/nexus-engine" ]]; then
  info "Building nexus-engine..."
  (cd "$REPO_ROOT/nexus-engine" && go build -o nexus-engine) || die "Failed to build nexus-engine"
fi

# Build CLI (Go)
if [[ ! -f "$REPO_ROOT/nexus-cli/nexus" ]]; then
  info "Building nexus-cli..."
  (cd "$REPO_ROOT/nexus-cli" && go build -o nexus) || die "Failed to build nexus-cli"
fi

# Build node agent (Rust)
if [[ ! -f "$REPO_ROOT/nexus-node-agent/target/release/nexus-node-agent" ]]; then
  info "Building nexus-node-agent..."
  (cd "$REPO_ROOT/nexus-node-agent" && cargo build --release) || die "Failed to build node agent"
fi

ok "All components built"

# ─── Phase 1: System packages ────────────────────────────────────────────────
banner "Phase 1: System Packages"

if [[ "$PKG_MGR" == "unknown" ]]; then
  die "No supported package manager found (apt/dnf/yum/pacman/zypper). Install packages manually."
fi

pkg_update
pkg_install curl wget jq git ca-certs
pkg_install iptables ipset
pkg_install wireguard

pkg_install build-essential
pkg_install golang
pkg_install rust cargo

# Only install the Redis host package when Redis-on-host mode is chosen.
# In Docker mode the container manages itself.
if [[ "${NEXUS_REDIS_BACKEND:-docker}" == "host" ]]; then
  pkg_install redis
fi

# Resolve the Redis systemd service name (used in Phases 5 + 8).
REDIS_SVC="redis-server"
[[ "$PKG_MGR" != "apt" ]] && REDIS_SVC="redis"

ok "System packages installed"

# ─── Phase 2: k3s ─────────────────────────────────────────────────────────────
banner "Phase 2: k3s"
if ! command -v k3s &>/dev/null; then
  info "Installing k3s…"
  curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--disable=traefik" sh -
fi
ok "k3s: $(k3s --version | head -1)"

info "Waiting for k3s API…"
for i in $(seq 1 30); do
  k3s kubectl get nodes &>/dev/null && break
  sleep 2
  [[ $i -eq 30 ]] && die "k3s not ready after 60s"
done
ok "k3s ready"

k3s kubectl create namespace "$NEXUS_K3S_NAMESPACE" --dry-run=client -o yaml | k3s kubectl apply -f - >/dev/null
mkdir -p /root/.kube
[[ -f /root/.kube/config ]] || cp /etc/rancher/k3s/k3s.yaml /root/.kube/config
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml

# ─── Phase 3: nerdctl ─────────────────────────────────────────────────────────
banner "Phase 3: nerdctl"
if ! command -v nerdctl &>/dev/null; then
  NV="1.7.6"; NA="amd64"; [[ "$ARCH" == "aarch64" ]] && NA="arm64"
  TMPDIR=$(mktemp -d)
  curl -fsSL "https://github.com/containerd/nerdctl/releases/download/v${NV}/nerdctl-full-${NV}-linux-${NA}.tar.gz" \
    -o "$TMPDIR/nerdctl.tar.gz"
  tar -xzf "$TMPDIR/nerdctl.tar.gz" -C /usr/local
  rm -rf "$TMPDIR"
fi
ok "nerdctl: $(nerdctl --version)"

# --- Phase 3.1: Permissions (Nexus Group) -------------------------------------
banner "Phase 3.1: Permissions"
info "Configuring nexus group for containerd access…"
groupadd -f nexus
# Add the SUDO_USER (the real person) to the group, not just root.
USER_TO_ADD="${SUDO_USER:-$USER}"
usermod -aG nexus "$USER_TO_ADD"
ok "User '$USER_TO_ADD' added to group 'nexus'"

SOCKET="/run/k3s/containerd/containerd.sock"
if [[ -S "$SOCKET" ]]; then
  chown root:nexus "$SOCKET"
  chmod 660 "$SOCKET"
  ok "Containerd socket permissions set (root:nexus)"
else
  warn "Socket $SOCKET not found yet — will apply via systemd trigger"
fi

# Create a systemd path unit to ensure permissions are reapplied if k3s restarts.
cat > /etc/systemd/system/nexus-socket-fix.service <<EOF
[Unit]
Description=Fix Nexus Containerd Socket Permissions
After=k3s.service

[Service]
Type=oneshot
ExecStart=/usr/bin/chown root:nexus $SOCKET
ExecStart=/usr/bin/chmod 660 $SOCKET
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/nexus-socket-fix.path <<EOF
[Unit]
Description=Watch Nexus Containerd Socket

[Path]
PathExists=$SOCKET

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now nexus-socket-fix.path
ok "Socket permission persistence enabled (systemd-path)"

# ─── Phase 3.5: BuildKit ──────────────────────────────────────────────────────
if ! systemctl is-active buildkit &>/dev/null; then
  info "Configuring BuildKit daemon..."
  cat > /etc/systemd/system/buildkit.service <<EOF
[Unit]
Description=BuildKit
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/buildkitd
Restart=always

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now buildkit
  ok "BuildKit started"
fi

banner "Phase 4: Container Registry"

case "${NEXUS_REGISTRY_TYPE:-local}" in

  # Option 1: standalone nerdctl container on the host.
  # Avoids the k3s NodePort restriction (30000-32767) entirely.
  local)
    LOCAL_REG_PORT="${NEXUS_REGISTRY_PORT:-5000}"
    LOCAL_REG_NAME="nexus-registry"
    if nerdctl inspect "$LOCAL_REG_NAME" &>/dev/null; then
      ok "Registry container '$LOCAL_REG_NAME' already running"
    else
      info "Starting local registry on port $LOCAL_REG_PORT..."
      nerdctl run -d \
        --name "$LOCAL_REG_NAME" \
        --restart always \
        -p "${LOCAL_REG_PORT}:5000" \
        -v /var/lib/nexus-registry:/var/lib/registry \
        registry:2
      ok "Registry started -> http://localhost:$LOCAL_REG_PORT"
    fi
    mkdir -p /etc/rancher/k3s
    cat > /etc/rancher/k3s/registries.yaml <<YAML
mirrors:
  "localhost:${LOCAL_REG_PORT}":
    endpoint: ["http://localhost:${LOCAL_REG_PORT}"]
  "127.0.0.1:${LOCAL_REG_PORT}":
    endpoint: ["http://127.0.0.1:${LOCAL_REG_PORT}"]
YAML
    ok "containerd mirror configured for localhost:$LOCAL_REG_PORT"
    ;;

  # Option 2: Docker Hub
  dockerhub)
    if [[ -n "${NEXUS_REGISTRY_USER:-}" && -n "${NEXUS_REGISTRY_PASS:-}" ]]; then
      echo "$NEXUS_REGISTRY_PASS" | nerdctl login docker.io -u "$NEXUS_REGISTRY_USER" --password-stdin \
        && ok "Logged in to Docker Hub as $NEXUS_REGISTRY_USER" \
        || warn "Docker Hub login failed"
    else
      warn "No Docker Hub credentials - set NEXUS_REGISTRY_USER / NEXUS_REGISTRY_PASS"
    fi
    ;;

  # Option 3: GitHub Container Registry
  ghcr)
    if [[ -n "${NEXUS_REGISTRY_USER:-}" && -n "${NEXUS_REGISTRY_PASS:-}" ]]; then
      echo "$NEXUS_REGISTRY_PASS" | nerdctl login ghcr.io -u "$NEXUS_REGISTRY_USER" --password-stdin \
        && ok "Logged in to GHCR as $NEXUS_REGISTRY_USER" \
        || warn "GHCR login failed - check PAT has write:packages scope"
    else
      warn "No GHCR credentials - set NEXUS_REGISTRY_USER / NEXUS_REGISTRY_PASS"
    fi
    ;;

  # Option 4: AWS ECR
  ecr)
    if command -v aws &>/dev/null; then
      info "Logging in to ECR ($NEXUS_REGISTRY_URL)..."
      aws ecr get-login-password --region "${NEXUS_ECR_REGION:-us-east-1}" \
        | nerdctl login "$NEXUS_REGISTRY_URL" -u AWS --password-stdin \
        && ok "Logged in to AWS ECR" \
        || warn "ECR login failed - check IAM permissions"
    else
      warn "aws CLI not found. Run manually:"
      warn "  aws ecr get-login-password | nerdctl login $NEXUS_REGISTRY_URL -u AWS --password-stdin"
    fi
    ;;

  # Option 5: custom registry
  custom)
    if [[ -n "${NEXUS_REGISTRY_USER:-}" && -n "${NEXUS_REGISTRY_PASS:-}" ]]; then
      echo "$NEXUS_REGISTRY_PASS" | nerdctl login "$NEXUS_REGISTRY_URL" \
        -u "$NEXUS_REGISTRY_USER" --password-stdin \
        && ok "Logged in to $NEXUS_REGISTRY_URL" \
        || warn "Login to $NEXUS_REGISTRY_URL failed"
    else
      info "No credentials for custom registry - assuming public or pre-authenticated"
    fi
    ;;
esac

# ─── Phase 5: Redis ───────────────────────────────────────────────────────────
banner "Phase 5: Redis"

if [[ "${NEXUS_REDIS_BACKEND:-docker}" == "docker" ]]; then
  REDIS_CONTAINER="nexus-redis"
  if nerdctl inspect "$REDIS_CONTAINER" &>/dev/null; then
    ok "Redis container '$REDIS_CONTAINER' already running"
  else
    info "Starting Redis Docker container..."
    nerdctl run -d \
      --name "$REDIS_CONTAINER" \
      --restart always \
      -p 6379:6379 \
      redis:7-alpine
    ok "Redis container started on port 6379"
  fi
else
  systemctl enable "$REDIS_SVC" --quiet && systemctl start "$REDIS_SVC"
fi

sleep 1
redis-cli -u "${NEXUS_REDIS_URL}" ping 2>/dev/null | grep -q PONG \
  && ok "Redis responding at $NEXUS_REDIS_URL" \
  || die "Redis not responding at $NEXUS_REDIS_URL"

# ─── Phase 6: WireGuard ───────────────────────────────────────────────────────
banner "Phase 6: WireGuard"
if [[ ! -f /etc/wireguard/wg0.conf ]]; then
  WG_KEY=$(wg genkey)
  WG_PUB=$(echo "$WG_KEY" | wg pubkey)
  mkdir -p /etc/wireguard && chmod 700 /etc/wireguard
  cat > /etc/wireguard/wg0.conf <<WGCONF
[Interface]
Address = 10.8.0.1/24
ListenPort = 51820
PrivateKey = $WG_KEY
PostUp = iptables -A FORWARD -i wg0 -j ACCEPT; iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
PostDown = iptables -D FORWARD -i wg0 -j ACCEPT; iptables -t nat -D POSTROUTING -o eth0 -j MASQUERADE
# Peers managed by nexus-node-agent
WGCONF
  chmod 600 /etc/wireguard/wg0.conf
  systemctl enable wg-quick@wg0 --quiet
  systemctl start wg-quick@wg0 || warn "wg-quick start failed"
  echo ""
  info "WireGuard server public key:"
  echo "  $WG_PUB"
else
  ok "WireGuard config already exists"
fi

# ─── Phase 7: Nexus binaries ──────────────────────────────────────────────────
banner "Phase 7: Nexus Binaries"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"

install_if_exists() {
  local src="$1" dst="$2" name="$3"
  if [[ -f "$src" ]]; then
    install -m 755 "$src" "$dst"
    ok "$name installed → $dst"
  elif command -v "$name" &>/dev/null; then
    ok "$name already in PATH"
  else
    warn "$name not found — build first (see README.md)"
  fi
}

install_if_exists "$REPO_ROOT/nexus-engine/nexus-engine" /usr/local/bin/nexus-engine nexus-engine
install_if_exists "$REPO_ROOT/nexus-cli/nexus"         /usr/local/bin/nexus         nexus

# Node agent: prefer release binary, fall back to debug, warn only if neither exists.
install_node_agent() {
  local release="$REPO_ROOT/nexus-node-agent/target/release/nexus-node-agent"
  local debug_bin="$REPO_ROOT/nexus-node-agent/target/debug/nexus-node-agent"
  local dst="/usr/local/bin/nexus-node-agent"
  if [[ -f "$release" ]]; then
    install -m 755 "$release" "$dst"
    ok "nexus-node-agent installed (release) -> $dst"
  elif [[ -f "$debug_bin" ]]; then
    install -m 755 "$debug_bin" "$dst"
    warn "nexus-node-agent installed (debug build) -> $dst"
  elif command -v nexus-node-agent &>/dev/null; then
    ok "nexus-node-agent already in PATH"
  else
    warn "nexus-node-agent not found - build first: cd nexus-node-agent && cargo build --release"
  fi
}
install_node_agent

# ─── Phase 8: Systemd units ───────────────────────────────────────────────────
banner "Phase 8: Systemd Services"

cat > /etc/systemd/system/nexus-node-agent.service <<EOF
[Unit]
Description=Nexus OSS Node Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/nexus-node-agent
Restart=on-failure
RestartSec=5
Environment=NEXUS_MODE=${NEXUS_MODE}
Environment=NODE_AGENT_LISTEN_ADDR=0.0.0.0:50051
Environment=NODE_AGENT_INSECURE=true
Environment=RUST_LOG=nexus_node_agent=info,info
AmbientCapabilities=CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_ADMIN
StandardOutput=journal
SyslogIdentifier=nexus-node-agent

[Install]
WantedBy=multi-user.target
EOF

cat > /etc/systemd/system/nexus-engine.service <<EOF
[Unit]
Description=Nexus OSS Engine
After=network.target ${REDIS_SVC}.service nexus-node-agent.service
Requires=${REDIS_SVC}.service

[Service]
Type=simple
ExecStart=/usr/local/bin/nexus-engine
Restart=on-failure
RestartSec=5
Environment=NEXUS_MODE=${NEXUS_MODE}
Environment=NEXUS_PORT=${NEXUS_PORT}
Environment=NEXUS_REDIS_URL=${NEXUS_REDIS_URL}
Environment=NEXUS_REGISTRY_URL=${NEXUS_REGISTRY_URL}
Environment=NEXUS_NODE_AGENT_ADDR=${NEXUS_NODE_AGENT_ADDR}
Environment=NEXUS_K3S_NAMESPACE=${NEXUS_K3S_NAMESPACE}
Environment=NEXUS_NODE_AGENT_INSECURE=true
Environment=KUBECONFIG=/etc/rancher/k3s/k3s.yaml
StandardOutput=journal
SyslogIdentifier=nexus-engine

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload

for svc in nexus-node-agent nexus-engine; do
  if command -v "${svc/nexus-/nexus-}" &>/dev/null || [[ -f "/usr/local/bin/$svc" ]]; then
    systemctl enable "$svc" --quiet
    systemctl restart "$svc"
    sleep 2
    systemctl is-active "$svc" &>/dev/null && ok "$svc running" || warn "$svc failed — journalctl -u $svc -n 30"
  fi
done

# ─── Phase 9: Network Policies ──────────────────────────────────────────────────
banner "Phase 9: Network Policies"

# Generate the policy inline so it's always correct regardless of whether the
# external file is present or was accidentally left empty.
if [[ "$NEXUS_MODE" == "prod" ]]; then
  info "Applying prod network policy (VPN-only ingress)..."
  k3s kubectl apply -f - <<NPPROD
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: nexus-prod-isolate
  namespace: ${NEXUS_K3S_NAMESPACE}
  labels:
    managed-by: nexus
    mode: prod
spec:
  podSelector:
    matchLabels:
      app: nexus-challenge
  ingress:
    - from:
        - ipBlock:
            cidr: 10.8.0.0/24
  egress:
    - {}
  policyTypes:
    - Ingress
    - Egress
NPPROD
  ok "Prod network policy applied (WireGuard VPN ingress only)"
else
  info "Applying dev network policy (allow-all)..."
  k3s kubectl apply -f - <<NPDEV
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: nexus-dev-allow-all
  namespace: ${NEXUS_K3S_NAMESPACE}
  labels:
    managed-by: nexus
    mode: dev
spec:
  podSelector: {}
  ingress:
    - {}
  egress:
    - {}
  policyTypes:
    - Ingress
    - Egress
NPDEV
  ok "Dev network policy applied (allow-all)"
fi

# ─── Done ─────────────────────────────────────────────────────────────────────
sleep 2
ENGINE_URL="http://localhost:${NEXUS_PORT}"
echo ""
if curl -sf "$ENGINE_URL/health" | jq -r '.status' 2>/dev/null | grep -q healthy; then
  ok "Engine health check passed"
fi

echo ""
echo -e "${GREEN}${BOLD}╔══════════════════════════════════════╗${NC}"
echo -e "${GREEN}${BOLD}║  Nexus OSS installed successfully!   ║${NC}"
echo -e "${GREEN}${BOLD}╚══════════════════════════════════════╝${NC}"
echo ""
echo "  Engine:   http://localhost:${NEXUS_PORT}"
echo "  Agent:    grpc://localhost:50051"
echo "  Registry: http://${NEXUS_REGISTRY_URL}"
echo ""
echo "Quick start:"
echo "  nexus status"
echo "  nexus challenge register --name pwn-101 --dockerfile ./challenges/pwn-101/Dockerfile"
echo "  nexus session create --challenge pwn-101 --user alice"
echo "  nexus tui"
echo "  bash scripts/smoke_test.sh"
echo ""
