#!/bin/bash
# install-linux.sh — build and install the AIGuard daemon on Linux/Ubuntu
#
# Usage:
#   ./install-linux.sh          # build + install to /usr/local/bin
#   ./install-linux.sh --local  # build only, keep binary in current directory
#
# Requirements:
#   - Go 1.21+  (https://go.dev/dl/)
#   - git

set -euo pipefail

BINARY="aiguard"
INSTALL_DIR="/usr/local/bin"
LOG="/tmp/aiguard-daemon.log"
LOCAL_ONLY=false

# ── parse flags ───────────────────────────────────────────────────────────────

for arg in "$@"; do
  case "$arg" in
    --local) LOCAL_ONLY=true ;;
    *) echo "Unknown flag: $arg"; exit 1 ;;
  esac
done

# ── checks ────────────────────────────────────────────────────────────────────

echo "── AIGuard Linux installer ──────────────────────────────"

if ! command -v go &> /dev/null; then
  echo "❌  Go not found. Install from https://go.dev/dl/ then re-run."
  exit 1
fi

GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
echo "✅  Go $GO_VERSION found"

# ── build ─────────────────────────────────────────────────────────────────────

echo ""
echo "Building daemon..."
go build -o "$BINARY" ./cmd/aiguard/
echo "✅  Built: $(pwd)/$BINARY"

# ── install ───────────────────────────────────────────────────────────────────

if [ "$LOCAL_ONLY" = false ]; then
  echo ""
  echo "Installing to $INSTALL_DIR/$BINARY (may require sudo)..."
  if [ -w "$INSTALL_DIR" ]; then
    cp "$BINARY" "$INSTALL_DIR/$BINARY"
  else
    sudo cp "$BINARY" "$INSTALL_DIR/$BINARY"
  fi
  chmod +x "$INSTALL_DIR/$BINARY"
  echo "✅  Installed: $INSTALL_DIR/$BINARY"
fi

# ── optional: notify-send check ───────────────────────────────────────────────

echo ""
if command -v notify-send &> /dev/null; then
  echo "✅  notify-send found — desktop notifications enabled"
else
  echo "⚠️   notify-send not found — notifications will be skipped"
  echo "    Install with: sudo apt install libnotify-bin"
fi

# ── usage summary ─────────────────────────────────────────────────────────────

echo ""
echo "── Done! ────────────────────────────────────────────────"
echo ""
echo "Start the daemon:"
if [ "$LOCAL_ONLY" = true ]; then
  echo "  ./aiguard daemon > $LOG 2>&1 &"
else
  echo "  aiguard daemon > $LOG 2>&1 &"
fi
echo ""
echo "Open the dashboard:"
echo "  http://localhost:7474"
echo ""
echo "Other commands:"
echo "  aiguard status          — list detected AI processes"
echo "  aiguard kill            — terminate all processes marked TERMINATE"
echo "  aiguard list            — show all tools and their settings"
echo "  aiguard enable  all     — arm every tool"
echo "  aiguard disable all     — disarm every tool"
echo "  aiguard alerts          — show recent alerts"
echo "─────────────────────────────────────────────────────────"
