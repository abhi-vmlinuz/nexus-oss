#!/usr/bin/env bash
set -e

echo "[*] Nexus OSS Bootstrap"

ARCH=$(uname -m)
OS=$(uname -s)

if [[ "$OS" != "Linux" ]]; then
  echo "[!] Only Linux is supported for now"
  exit 1
fi

case "$ARCH" in
  x86_64) BIN="nexus-installer-linux-amd64" ;;
  aarch64) BIN="nexus-installer-linux-arm64" ;;
  *) echo "[!] Unsupported architecture: $ARCH"; exit 1 ;;
esac

URL="https://github.com/abhi-vmlinuz/nexus-oss/releases/latest/download/$BIN"

echo "[*] Detected: $OS / $ARCH"
echo "[*] Downloading installer..."

curl -fL --retry 3 --retry-delay 2 "$URL" -o nexus-installer

chmod +x nexus-installer

echo "[*] Launching installer..."
./nexus-installer

echo "[*] Cleaning up..."
rm -f nexus-installer

echo "[✓] Done"
