#!/bin/bash

# macOS full deployment script: build + install + restart daemon + restart tray app
# Usage: ./deploy-macos.sh
#
# Requirements:
#   - Go (brew install go)
#   - Xcode Command Line Tools (xcode-select --install)
#
# This script:
#   1. Builds the daemon (Go)
#   2. Builds the menu bar app (Swift)
#   3. Installs both to /Applications/AIGuard.app
#   4. Restarts the daemon and menu bar app

set -e

echo "🔨 Building daemon..."
go build -o aiguard ./cmd/aiguard/

echo "🔨 Building menu bar app..."
swiftc -o aiguard-tray cmd/aiguard-tray/MenuBar.swift

echo "📦 Installing..."
./install-macos.sh

echo ""
echo "🔄 Restarting daemon..."
pkill -f "aiguard daemon" 2>/dev/null || true
sleep 1
./aiguard daemon > /tmp/aiguard-daemon.log 2>&1 &

echo "🔄 Restarting menu bar app..."
pkill -x AIGuard 2>/dev/null || true
sleep 2
# open -n forces a fresh launch even if macOS thinks the app is already running
# This is required for AppKit/NSStatusBar to properly connect to the WindowServer
open -n "/Applications/AIGuard.app"
sleep 3

echo ""
echo "── Verification ─────────────────────────"
if pgrep -la aiguard > /dev/null 2>&1; then
  echo "✅  Daemon running: $(pgrep -la aiguard)"
else
  echo "❌  Daemon NOT running — check /tmp/aiguard-daemon.log"
fi

if pgrep -la AIGuard > /dev/null 2>&1; then
  echo "✅  Menu bar running: $(pgrep -la AIGuard)"
else
  echo "❌  Menu bar NOT running"
fi
echo "─────────────────────────────────────────"
