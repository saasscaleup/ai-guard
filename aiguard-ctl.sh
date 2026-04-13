#!/bin/bash
# aiguard-ctl.sh — start / stop / restart / status for AIGuard daemon + tray
#
# Usage:
#   ./aiguard-ctl.sh start
#   ./aiguard-ctl.sh stop
#   ./aiguard-ctl.sh restart
#   ./aiguard-ctl.sh status
#   ./aiguard-ctl.sh logs

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DAEMON="$SCRIPT_DIR/aiguard"
APP="/Applications/AIGuard.app"
LOG="/tmp/aiguard-daemon.log"

# ── helpers ───────────────────────────────────────────────────────────────────

daemon_running() { pgrep -f "aiguard daemon" > /dev/null 2>&1; }
tray_running()   { pgrep -x AIGuard          > /dev/null 2>&1; }

start_daemon() {
  if daemon_running; then
    echo "  daemon  already running (PID $(pgrep -f 'aiguard daemon'))"
  else
    "$DAEMON" daemon >> "$LOG" 2>&1 &
    sleep 1
    if daemon_running; then
      echo "  daemon  ✅  started (PID $(pgrep -f 'aiguard daemon'))"
    else
      echo "  daemon  ❌  failed to start — check $LOG"
    fi
  fi
}

stop_daemon() {
  if daemon_running; then
    pkill -f "aiguard daemon"
    echo "  daemon  stopped"
  else
    echo "  daemon  already stopped"
  fi
}

start_tray() {
  if ! [ -d "$APP" ]; then
    echo "  tray    ❌  $APP not found — run ./deploy.sh first"
    return
  fi
  if tray_running; then
    echo "  tray    already running (PID $(pgrep -x AIGuard))"
  else
    open -n "$APP"
    sleep 2
    if tray_running; then
      echo "  tray    ✅  started (PID $(pgrep -x AIGuard))"
    else
      echo "  tray    ❌  failed to start"
    fi
  fi
}

stop_tray() {
  if tray_running; then
    pkill -x AIGuard
    echo "  tray    stopped"
  else
    echo "  tray    already stopped"
  fi
}

# ── commands ──────────────────────────────────────────────────────────────────

cmd="${1:-}"

case "$cmd" in
  start)
    echo "Starting AIGuard..."
    start_daemon
    start_tray
    ;;

  stop)
    echo "Stopping AIGuard..."
    stop_daemon
    stop_tray
    ;;

  restart)
    echo "Restarting AIGuard..."
    stop_daemon
    stop_tray
    sleep 1
    start_daemon
    start_tray
    ;;

  status)
    echo "AIGuard status:"
    if daemon_running; then
      echo "  daemon  ✅  running (PID $(pgrep -f 'aiguard daemon'))"
    else
      echo "  daemon  ⏹   stopped"
    fi
    if tray_running; then
      echo "  tray    ✅  running (PID $(pgrep -x AIGuard))"
    else
      echo "  tray    ⏹   stopped"
    fi
    echo "  logs    $LOG"
    ;;

  logs)
    if [ -f "$LOG" ]; then
      tail -f "$LOG"
    else
      echo "No log file yet at $LOG — start the daemon first."
    fi
    ;;

  *)
    echo "Usage: $0 {start|stop|restart|status|logs}"
    exit 1
    ;;
esac
