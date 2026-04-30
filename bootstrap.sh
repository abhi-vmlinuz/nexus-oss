#!/usr/bin/env bash
set -euo pipefail

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

BASE_URL="https://github.com/abhi-vmlinuz/nexus-oss/releases/latest/download"

echo "[*] Detected: $OS / $ARCH"

echo "[*] Downloading installer..."
curl -fL --retry 3 --retry-delay 2 "$BASE_URL/$BIN" -o nexus-installer

echo "[*] Downloading checksums..."
curl -fL --retry 3 --retry-delay 2 "$BASE_URL/checksums.txt" -o checksums.txt

echo "[*] Verifying integrity..."

EXPECTED=$(grep "$BIN" checksums.txt | awk '{print $1}')
ACTUAL=$(sha256sum nexus-installer | awk '{print $1}')

if [[ "$EXPECTED" != "$ACTUAL" ]]; then
  echo "[!] Checksum verification failed!"
  exit 1
fi

echo "[✓] Checksum verified"

chmod +x nexus-installer

echo "[*] Launching installer..."
./nexus-installer

echo "[*] Cleaning up..."
rm -f nexus-installer checksums.txt

echo "[✓] Done"
