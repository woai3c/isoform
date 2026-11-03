#!/usr/bin/env bash
# Watch Yansu CPU and capture a 30-second pprof profile when it bursts.
#
# Usage:
#   YANSU_PPROF=1 ./wails-gui/scripts/start-wails-gui.sh   # in one terminal
#   ./wails-gui/scripts/profile-on-burst.sh                # in another terminal
#
# Set THRESHOLD env var to override the CPU threshold (default 80%).

set -euo pipefail

THRESHOLD="${THRESHOLD:-80}"
PPROF_PORT="${PPROF_PORT:-6060}"
OUT_DIR="/tmp/yansu-pprof"
mkdir -p "$OUT_DIR"

echo "[profile-on-burst] watching for Yansu CPU > ${THRESHOLD}%..."
echo "[profile-on-burst] pprof endpoint: http://127.0.0.1:${PPROF_PORT}/debug/pprof/"
echo "[profile-on-burst] output dir: ${OUT_DIR}"
echo ""

# Sanity check pprof is reachable.
if ! curl -fsS --max-time 2 "http://127.0.0.1:${PPROF_PORT}/debug/pprof/" > /dev/null; then
    echo "[profile-on-burst] ERROR: pprof endpoint not reachable on port ${PPROF_PORT}"
    echo "[profile-on-burst] Did you start Yansu with YANSU_PPROF=1?"
    exit 1
fi
echo "[profile-on-burst] pprof endpoint OK"
echo ""

while true; do
    # Find the Yansu process (built or dev — both end with /MacOS/Yansu).
    PID=$(pgrep -f '/MacOS/Yansu$' | head -1 || true)
    if [ -z "$PID" ]; then
        sleep 2
        continue
    fi

    # Sample %CPU twice 1s apart and average — ps gives instantaneous.
    CPU=$(ps -p "$PID" -o %cpu= | awk '{print int($1)}')
    if [ -z "$CPU" ]; then
        sleep 2
        continue
    fi

    if [ "$CPU" -ge "$THRESHOLD" ]; then
        TS=$(date +%Y%m%d-%H%M%S)
        OUT="${OUT_DIR}/profile-${TS}-cpu${CPU}.pb.gz"
        echo "[$(date +%H:%M:%S)] BURST: PID=${PID} CPU=${CPU}% -> capturing 30s profile to ${OUT}"
        if curl -fsS --max-time 60 -o "$OUT" \
            "http://127.0.0.1:${PPROF_PORT}/debug/pprof/profile?seconds=30"; then
            echo "[$(date +%H:%M:%S)] captured. Analyze with:"
            echo "  go tool pprof -text -cum ${OUT} | head -30"
            echo "  go tool pprof -http=:8080 ${OUT}"
        else
            echo "[$(date +%H:%M:%S)] capture failed"
        fi
        # Cool-down to avoid back-to-back captures.
        sleep 60
    else
        echo "[$(date +%H:%M:%S)] PID=${PID} CPU=${CPU}% (waiting for burst)"
        sleep 5
    fi
done
