#!/bin/bash
set -e

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}Nexus OSS Installer Bootstrap${NC}"

# 0. Request Sudo up front
echo -e "${BLUE}The installer requires root privileges for system configuration.${NC}"
sudo -v

# Keep-alive: update existing sudo time stamp until the script has finished
while true; do sudo -n true; sleep 60; kill -0 "$$" || exit; done 2>/dev/null &

echo -e "${BLUE}Building Nexus TUI Installer...${NC}"
cd "$(dirname "$0")/nexus-installer"

# 1. Build
go build -o nexus-installer *.go

# 2. Install
echo -e "${BLUE}Installing to /usr/local/bin...${NC}"
sudo install -m 755 nexus-installer /usr/local/bin/nexus-installer

# 3. Run (as user, using cached sudo for internal commands)
echo -e "${GREEN}Launching Nexus Installer...${NC}"
/usr/local/bin/nexus-installer

INSTALL_STATUS=$?

# 4. Cleanup
if [ $INSTALL_STATUS -eq 0 ]; then
    echo -e "${BLUE}Installation complete. Cleaning up installer binary...${NC}"
    sudo rm /usr/local/bin/nexus-installer
    echo -e "${GREEN}Cleanup finished. Nexus OSS is ready!${NC}"
else
    echo -e "${RED}Installer exited with error.${NC}"
    echo -e "Keeping binary for debugging at: /usr/local/bin/nexus-installer"
    exit 1
fi
