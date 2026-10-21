#!/bin/sh
set -eu

REPO="eugenioenko/ttt-mcp"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac
case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported OS: $OS; download the Windows binary from GitHub Releases" >&2; exit 1 ;;
esac

ASSET="ttt-mcp-${OS}-${ARCH}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
case "$INSTALL_DIR" in
  /*) ;;
  *) INSTALL_DIR="$(pwd)/$INSTALL_DIR" ;;
esac
VERSION="${1:-latest}"
if [ "$VERSION" = latest ]; then
  BASE="https://github.com/${REPO}/releases/latest/download"
else
  BASE="https://github.com/${REPO}/releases/download/${VERSION}"
fi

STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT HUP INT TERM
curl -fsSL "${BASE}/${ASSET}" -o "${STAGING}/${ASSET}"
curl -fsSL "${BASE}/checksums.txt" -o "${STAGING}/checksums.txt"
cd "$STAGING"
awk -v asset="$ASSET" '$2 == asset { print }' checksums.txt > selected-checksum.txt
test -s selected-checksum.txt
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum -c selected-checksum.txt
elif command -v shasum >/dev/null 2>&1; then
  shasum -a 256 -c selected-checksum.txt
else
  echo "Install sha256sum or shasum to verify the download" >&2
  exit 1
fi
chmod +x "$ASSET"
mkdir -p "$INSTALL_DIR"
mv "$ASSET" "${INSTALL_DIR}/ttt-mcp"
"${INSTALL_DIR}/ttt-mcp" install-plugin
echo "Installed ttt-mcp to ${INSTALL_DIR}/ttt-mcp"
echo "Ensure ${INSTALL_DIR} is on PATH, then restart ttt and approve the plugin."
