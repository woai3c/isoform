#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_NAME="${1:-Yansu}"
DURATION_SECONDS="${2:-60}"
INTERVAL_SECONDS="${3:-0.5}"
COOLDOWN_SECONDS="${4:-10}"
RUN_ID="$(date +%Y%m%d-%H%M%S)"
OUT_DIR="/tmp/yansu-cpu-comparison-${RUN_ID}"
SUMMARY_FILE="${OUT_DIR}/summary.csv"

mkdir -p "$OUT_DIR"

summarize_cpu() {
    local scenario="$1"
    local csv_file="$2"
    local action_output="$3"

    awk -F, -v scenario="$scenario" -v action_output="$action_output" '
        NR == FNR {
            if ($0 ~ /^Mode / || $0 ~ /^Samples:/) {
                action = action $0 " "
            }
            next
        }
        FNR > 1 {
            n++
            cpu[n] = $3
            sum += $3
            if ($3 > max) max = $3
            if ($3 >= 100) ge100++
            if ($3 >= 200) ge200++
            if ($3 >= 300) ge300++
            if ($3 >= 400) ge400++
            if (n == 1) {
                first_rss = $4
                min_rss = $4
                max_rss = $4
            }
            last_rss = $4
            if ($4 < min_rss) min_rss = $4
            if ($4 > max_rss) max_rss = $4
        }
        END {
            for (i = 1; i <= n; i++) {
                for (j = i + 1; j <= n; j++) {
                    if (cpu[i] > cpu[j]) {
                        temp = cpu[i]
                        cpu[i] = cpu[j]
                        cpu[j] = temp
                    }
                }
            }
            if (n == 0) {
                printf "%s,0,0,0,0,0,0,0,0,0,0,0,0,0,0,\"%s\"\n", scenario, action
                exit
            }
            p50 = cpu[int((n * 50 + 99) / 100)]
            p90 = cpu[int((n * 90 + 99) / 100)]
            p95 = cpu[int((n * 95 + 99) / 100)]
            p99 = cpu[int((n * 99 + 99) / 100)]
            gsub(/"/, "\"\"", action)
            printf "%s,%d,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%d,%d,%d,%d,%.1f,%.1f,%.1f,\"%s\"\n", scenario, n, sum / n, max, p50, p90, p95, p99, ge100 + 0, ge200 + 0, ge300 + 0, ge400 + 0, first_rss / 1024, max_rss / 1024, last_rss / 1024, action
        }
    ' "$action_output" "$csv_file"
}

run_idle() {
    local scenario="idle"
    local csv_file="${OUT_DIR}/${scenario}.csv"
    local output_file="${OUT_DIR}/${scenario}.out"

    "$SCRIPT_DIR/watch-yansu-cpu.sh" "$APP_NAME" "$DURATION_SECONDS" "$INTERVAL_SECONDS" "$csv_file" > "$output_file" 2>&1
    summarize_cpu "$scenario" "$csv_file" "$output_file" >> "$SUMMARY_FILE"
}

run_mode() {
    local scenario="$1"
    local csv_file="${OUT_DIR}/${scenario}.csv"
    local output_file="${OUT_DIR}/${scenario}.out"

    "$SCRIPT_DIR/run-yansu-window-stress.sh" "$APP_NAME" "$DURATION_SECONDS" "$INTERVAL_SECONDS" "$csv_file" "$scenario" > "$output_file" 2>&1
    summarize_cpu "$scenario" "$csv_file" "$output_file" >> "$SUMMARY_FILE"
}

if ! pgrep -x "$APP_NAME" >/dev/null; then
    echo "No running process named '$APP_NAME' was found." >&2
    exit 1
fi

echo "scenario,samples,avg_cpu,max_cpu,p50_cpu,p90_cpu,p95_cpu,p99_cpu,samples_ge_100,samples_ge_200,samples_ge_300,samples_ge_400,first_rss_mb,max_rss_mb,last_rss_mb,action_summary" > "$SUMMARY_FILE"

echo "Writing comparison logs to $OUT_DIR"
run_idle
sleep "$COOLDOWN_SECONDS"
run_mode switch-only
sleep "$COOLDOWN_SECONDS"
run_mode move-only
sleep "$COOLDOWN_SECONDS"
run_mode full

cat "$SUMMARY_FILE"
