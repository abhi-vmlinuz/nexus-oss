#!/bin/bash
set -e

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}Nexus OSS - One-Click Bootstrapper${NC}"

# Check for git
if ! command -v git &>/dev/null; then
    echo -e "${BLUE}Git not found. Installing...${NC}"
    if command -v apt-get &>/dev/null; then sudo apt-get update && sudo apt-get install -y git;
    elif command -v dnf &>/dev/null; then sudo dnf install -y git;
    elif command -v pacman &>/dev/null; then sudo pacman -S --noconfirm --needed git;
    elif command -v zypper &>/dev/null; then sudo zypper install -y git;
    fi
fi

# Check for go (required to build the installer TUI)
if ! command -v go &>/dev/null; then
    echo -e "${BLUE}Go compiler not found. Installing...${NC}"
    if command -v apt-get &>/dev/null; then sudo apt-get update && sudo apt-get install -y golang-go;
    elif command -v dnf &>/dev/null; then sudo dnf install -y golang;
    elif command -v pacman &>/dev/null; then sudo pacman -S --noconfirm --needed go;
    elif command -v zypper &>/dev/null; then sudo zypper install -y go;
    fi
fi

# Clone to a temporary directory if not already in a repo
TEMP_DIR=$(mktemp -d)
echo -e "${BLUE}Cloning Nexus OSS to $TEMP_DIR...${NC}"
git clone https://github.com/abhi-vmlinuz/nexus-oss.git "$TEMP_DIR"

# Change to repo root
cd "$TEMP_DIR"

# Run the build-installer.sh
chmod +x build-installer.sh
./build-installer.sh

# Note: build-installer.sh handles cleanup of the binary, 
# but the source in $TEMP_DIR will remain unless we clean it here.
# However, the installer needs the source to build the engine/cli/agent.
# So we keep it until the installer finishes.

echo -e "${GREEN}Bootstrap finished successfully.${NC}"
