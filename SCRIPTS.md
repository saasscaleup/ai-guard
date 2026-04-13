# AIGuard Scripts Reference

This document explains what each deployment and installation script does.

## macOS Scripts

### `deploy-macos.sh` — Full macOS Deployment
**Build + Install + Restart**

```bash
./deploy-macos.sh
```

Complete end-to-end deployment for macOS:
1. Builds the daemon CLI (`aiguard`) using Go
2. Builds the menu bar app (`aiguard-tray`) using Swift
3. Installs both to `/Applications/AIGuard.app`
4. Removes macOS quarantine flags (allows app to launch without security warnings)
5. Restarts the daemon and menu bar app
6. Verifies both are running

**Requirements:**
- Go (`brew install go`)
- Xcode Command Line Tools (`xcode-select --install`)

**About code signing:** AIGuard is in alpha/testing and built without an Apple Developer certificate. macOS will show a warning on first launch. This script removes restrictions to allow launch. Once released publicly, we plan to obtain a certificate to code-sign the app. See the README for details.

**When to use:** Initial setup, major updates, or after changing code

---

### `install-macos.sh` — macOS Installation Only
**Install pre-built binaries**

```bash
./install-macos.sh
```

Installs pre-built binaries (assumes `aiguard` and `aiguard-tray` already exist):
1. Copies `aiguard-tray` to `/Applications/AIGuard.app`
2. Sets up the macOS app bundle structure (Info.plist)
3. Removes restrictions to allow launch (app is unsigned—in alpha)
4. Re-registers with macOS Launch Services

**Code signing:** AIGuard is in alpha/testing and built without an Apple Developer certificate. macOS will show a warning on first launch: *"AIGuard cannot be opened because it is from an unidentified developer."* This is normal and safe. The script removes restrictions to allow launch. Once released, we plan to obtain a certificate to code-sign the app.

**When to use:** After manually building binaries, or if you want to install without rebuilding

---

### `aiguard-ctl.sh` — Daemon Control
**Start/Stop/Restart the daemon**

```bash
./aiguard-ctl.sh start    # Start daemon
./aiguard-ctl.sh stop     # Stop daemon
./aiguard-ctl.sh restart  # Restart daemon
./aiguard-ctl.sh status   # Check if running
./aiguard-ctl.sh logs     # View daemon logs
```

Control script for the locally running daemon (after `deploy-macos.sh`).

**When to use:** Day-to-day daemon management

---

## Linux Scripts

### `install-linux.sh` — Linux Installation
**Build + Install (system-wide or local)**

```bash
# System-wide (requires sudo)
sudo ./install-linux.sh

# Local install to ~/.local/bin (no sudo)
./install-linux.sh --local
```

Builds and installs the daemon to:
- `/usr/local/bin/aiguard` (system-wide, requires sudo)
- `~/.local/bin/aiguard` (local, no sudo)

Automatically installs systemd service for system-wide installs.

**When to use:** Initial Linux setup

---

## VPS Scripts

### `deploy-vps.sh` — VPS Deployment
**Pull + Build + Install + Restart**

```bash
# System-wide (requires sudo)
./deploy-vps.sh

# Local install (no sudo)
./deploy-vps.sh --local
```

Complete VPS deployment workflow:
1. Pulls latest code from `git` (`origin/master`)
2. Builds the daemon binary
3. Installs to system or local directory
4. Restarts the daemon
5. Verifies daemon is running

**When to use:** Deploying updates to a running VPS

---

## Summary Table

| Script | OS | Purpose | Requires Build? |
|--------|----|---------|----|
| `deploy-macos.sh` | macOS | Full build + install + restart | Yes |
| `install-macos.sh` | macOS | Install pre-built binaries | No |
| `aiguard-ctl.sh` | macOS | Daemon start/stop/restart | No |
| `install-linux.sh` | Linux | Build + install | Yes |
| `deploy-vps.sh` | Linux (VPS) | Git pull + build + install + restart | Yes |

---

## Common Workflows

### macOS: First-Time Setup
```bash
./deploy-macos.sh
# Then use ./aiguard-ctl.sh for day-to-day management
```

### macOS: Rebuild After Code Changes
```bash
./deploy-macos.sh
```

### Linux VPS: Update to Latest Code
```bash
./deploy-vps.sh
```

### Linux VPS: Update Without System Install
```bash
./deploy-vps.sh --local
```
