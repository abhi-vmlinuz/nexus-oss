#!/usr/bin/env bash
# deploy/uninstaller.sh — Nexus OSS Uninstaller
#
# Cleans up Nexus binaries, services, and configurations.
# Leaves k3s intact.

set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; NC='\033[0m'

info() { echo -e "${GREEN}→${NC} $*"; }
warn() { echo -e "${YELLOW}⚠  $*${NC}"; }
die()  { echo -e "${RED}✗  $*${NC}" >&2; exit 1; }
ok()   { echo -e "${GREEN}✅ $*${NC}"; }

[[ $EUID -eq 0 ]] || die "Run as root: sudo bash uninstaller.sh"

echo -e "${RED}${BOLD}"
echo "╔══════════════════════════════════════╗"
echo "║      Nexus OSS Uninstaller           ║"
echo "╚══════════════════════════════════════╝"
echo -e "${NC}"

warn "This will remove Nexus Engine, CLI, Node Agent, and configurations."
read -rp "Are you sure you want to proceed? [y/N]: " confirm
[[ "${confirm,,}" == "y" ]] || { echo "Aborted."; exit 0; }

# 1. Stop and remove systemd services
info "Stopping and removing Nexus services..."
for svc in nexus-engine nexus-node-agent nexus-socket-fix.path nexus-socket-fix.service; do
    if systemctl is-active "$svc" &>/dev/null; then
        systemctl stop "$svc"
    fi
    if systemctl is-enabled "$svc" &>/dev/null; then
        systemctl disable "$svc"
    fi
    rm -f "/etc/systemd/system/$svc"
done
systemctl daemon-reload
ok "Services removed"

# 2. Remove binaries
info "Removing binaries..."
rm -f /usr/local/bin/nexus-engine
rm -f /usr/local/bin/nexus
rm -f /usr/local/bin/nexus-node-agent
rm -f /usr/local/bin/nerdctl
rm -f /usr/local/bin/buildkitd
rm -f /usr/local/bin/buildctl
ok "Binaries removed"

# 3. Clean up WireGuard
if [[ -f /etc/wireguard/wg0.conf ]]; then
    info "Cleaning up WireGuard wg0..."
    systemctl stop wg-quick@wg0 || true
    systemctl disable wg-quick@wg0 || true
    rm -f /etc/wireguard/wg0.conf
    ok "WireGuard cleaned"
fi

# 4. Remove configuration
info "Removing user configuration..."
CONF_USER="${SUDO_USER:-$USER}"
CONF_HOME=$(getent passwd "$CONF_USER" | cut -d: -f6)
rm -rf "$CONF_HOME/.config/nexus"
ok "User configuration removed"

# 5. Clean up Docker/Nerdctl containers
if command -v nerdctl &>/dev/null; then
    info "Stopping Nexus infrastructure containers..."
    nerdctl stop nexus-redis nexus-registry &>/dev/null || true
    nerdctl rm nexus-redis nexus-registry &>/dev/null || true
    ok "Containers removed"
fi

# 5.5 Optional K3s Namespace removal
if command -v k3s &>/dev/null; then
    echo ""
    warn "The Kubernetes namespace 'nexus-challenges' contains all active challenge pods."
    read -rp "Would you like to remove the k3s namespace 'nexus-challenges'? [y/N]: " del_ns
    if [[ "${del_ns,,}" == "y" ]]; then
        info "Removing k3s namespace..."
        k3s kubectl delete namespace nexus-challenges || true
        ok "Namespace removed"
    else
        info "Skipping namespace removal (data preserved)"
    fi
fi

# 6. Final cleanup
info "Removing nexus group..."
groupdel nexus &>/dev/null || true

echo ""
ok "Nexus OSS has been uninstalled."
warn "Note: k3s and system packages (curl, wireguard-tools, etc.) were left intact."
echo ""
