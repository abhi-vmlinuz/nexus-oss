#!/usr/bin/env bash
# build-installer.sh — Cross-compile Nexus Installer

set -e

VERSION="1.0.0"
DIST_DIR="dist"
mkdir -p "$DIST_DIR"

echo "Building Nexus Installer $VERSION..."

# Build for AMD64
echo "  → Building for linux/amd64..."
GOOS=linux GOARCH=amd64 go build -o "$DIST_DIR/nexus-installer-amd64" ./nexus-installer/*.go
sha256sum "$DIST_DIR/nexus-installer-amd64" > "$DIST_DIR/nexus-installer-amd64.sha256"

# Build for ARM64
echo "  → Building for linux/arm64..."
GOOS=linux GOARCH=arm64 go build -o "$DIST_DIR/nexus-installer-arm64" ./nexus-installer/*.go
sha256sum "$DIST_DIR/nexus-installer-arm64" > "$DIST_DIR/nexus-installer-arm64.sha256"

echo ""
echo "Build complete. Binaries located in $DIST_DIR/"
ls -lh "$DIST_DIR"
