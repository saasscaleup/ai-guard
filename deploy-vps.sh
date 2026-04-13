#!/bin/bash

# AIGuard VPS deployment script
# Usage: ./deploy-vps.sh [--local]
# --local: install locally (~/.local/bin), no sudo needed
# (default): install system-wide to /usr/local/bin, requires sudo

set -e

echo "=== AIGuard VPS Deployment ==="
echo ""

# Determine install mode
INSTALL_LOCAL=false
if [[ "$1" == "--local" ]]; then
    INSTALL_LOCAL=true
fi

# Pull latest code
echo "📥 Pulling latest code..."
git pull origin master

# Build binary
echo "🔨 Building aiguard binary..."
go build -o aiguard ./cmd/aiguard/
if [ ! -f aiguard ]; then
    echo "❌ Build failed"
    exit 1
fi
echo "✓ Build successful"

# Determine install path
if [ "$INSTALL_LOCAL" = true ]; then
    INSTALL_PATH="$HOME/.local/bin"
    mkdir -p "$INSTALL_PATH"
    echo "📦 Installing to $INSTALL_PATH (local)..."
    cp aiguard "$INSTALL_PATH/aiguard"
    chmod +x "$INSTALL_PATH/aiguard"
else
    echo "📦 Installing to /usr/local/bin (system-wide)..."
    sudo cp aiguard /usr/local/bin/aiguard
    sudo chmod +x /usr/local/bin/aiguard
fi
echo "✓ Installation successful"

# Restart daemon
echo "🔄 Restarting daemon..."
pkill -f "aiguard daemon" || true
sleep 1

if [ "$INSTALL_LOCAL" = true ]; then
    "$INSTALL_PATH/aiguard" daemon > /tmp/aiguard-daemon.log 2>&1 &
else
    /usr/local/bin/aiguard daemon > /tmp/aiguard-daemon.log 2>&1 &
fi

sleep 2

# Verify daemon is running
if pgrep -f "aiguard daemon" > /dev/null; then
    echo "✓ Daemon running (PID: $(pgrep -f 'aiguard daemon'))"
else
    echo "❌ Daemon failed to start"
    echo "Check logs: tail /tmp/aiguard-daemon.log"
    exit 1
fi

echo ""
echo "=== Deployment Complete ==="
echo "Daemon logs: tail -f /tmp/aiguard-daemon.log"
echo "Status: aiguard status"
echo ""
