#!/usr/bin/env bash
set -euo pipefail

APP_NAME="${1:-Yansu}"
DURATION_SECONDS="${2:-60}"
INTERVAL_SECONDS="${3:-0.5}"
LOG_FILE="${4:-/tmp/yansu-cpu-$(date +%Y%m%d-%H%M%S).csv}"

if ! pgrep -x "$APP_NAME" >/dev/null; then
    echo "No running process named '$APP_NAME' was found." >&2
    exit 1
fi

echo "timestamp,pid,cpu_percent,rss_kb,elapsed,command" > "$LOG_FILE"
echo "Sampling $APP_NAME CPU for ${DURATION_SECONDS}s every ${INTERVAL_SECONDS}s"
echo "Log: $LOG_FILE"

end_time=$(( $(date +%s) + DURATION_SECONDS ))

while [ "$(date +%s)" -lt "$end_time" ]; do
    timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    while IFS= read -r pid; do
        ps -p "$pid" -o %cpu= -o rss= -o etime= -o comm= | awk \
            -v timestamp="$timestamp" \
            -v pid="$pid" '
                NF >= 4 {
                    cpu=$1
                    rss=$2
                    elapsed=$3
                    $1=""
                    $2=""
                    $3=""
                    sub(/^ +/, "")
                    gsub(/"/, "\"\"")
                    printf "%s,%s,%.1f,%s,%s,\"%s\"\n", timestamp, pid, cpu, rss, elapsed, $0
                }
            '
    done < <(pgrep -x "$APP_NAME")
    sleep "$INTERVAL_SECONDS"
done | tee -a "$LOG_FILE"

awk -F, '
    NR > 1 {
        n++
        sum += $3
        if ($3 > max) max = $3
    }
    END {
        if (n == 0) {
            print "No samples collected."
            exit 1
        }
        printf "Samples: %d, average CPU: %.2f%%, max CPU: %.2f%%\n", n, sum / n, max
    }
' "$LOG_FILE"
