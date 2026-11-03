#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_NAME="${1:-Yansu}"
DURATION_SECONDS="${2:-60}"
INTERVAL_SECONDS="${3:-0.5}"
LOG_FILE="${4:-/tmp/yansu-cpu-$(date +%Y%m%d-%H%M%S).csv}"
MODE="${5:-full}"

if ! pgrep -x "$APP_NAME" >/dev/null; then
    echo "No running process named '$APP_NAME' was found." >&2
    exit 1
fi

"$SCRIPT_DIR/watch-yansu-cpu.sh" "$APP_NAME" "$DURATION_SECONDS" "$INTERVAL_SECONDS" "$LOG_FILE" &
watch_pid=$!

cleanup() {
    if kill -0 "$watch_pid" 2>/dev/null; then
        kill "$watch_pid" 2>/dev/null || true
        wait "$watch_pid" 2>/dev/null || true
    fi
}
trap cleanup INT TERM EXIT

osascript "$SCRIPT_DIR/yansu-window-stress.applescript" "$DURATION_SECONDS" "$APP_NAME" "$MODE"
wait "$watch_pid"
trap - INT TERM EXIT

echo "CPU log saved to $LOG_FILE"
