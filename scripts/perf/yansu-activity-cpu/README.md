# Yansu Activity CPU Stress Tools

These scripts reproduce and measure Yansu activity-monitor CPU behavior during
high-frequency macOS window switching and window movement.

Use them when investigating regressions in:

- Activity monitor event drain/backpressure
- Event-driven screenshot capture
- Visual-change polling
- Background OCR and OCR embedding after snapshot-heavy sessions

## Prerequisites

- macOS with Yansu running as process name `Yansu`
- Accessibility permission for the app running the AppleScript automation
- Accessibility and Screen Recording permissions for the Yansu build under test
- At least two visible, non-background apps with windows for `full` or
  `switch-only`; the script opens Finder and Calculator as helper targets when
  needed, then fails fast and prints the current candidates if it still cannot
  create real app switches

If the Yansu build under test has a different process name, pass it as the first
argument to the shell scripts.

## Scripts

### `watch-yansu-cpu.sh`

Samples CPU and RSS for every running process matching the app name.

```bash
scripts/perf/yansu-activity-cpu/watch-yansu-cpu.sh Yansu 60 0.5 /tmp/yansu-cpu.csv
```

Arguments:

1. App process name, default `Yansu`
2. Duration in seconds, default `60`
3. Sampling interval in seconds, default `0.5`
4. Output CSV path, default `/tmp/yansu-cpu-<timestamp>.csv`

### `yansu-window-stress.applescript`

Runs the actual UI stress loop through System Events. It can rapidly switch
frontmost windows, move windows, or do both.

Most callers should use `run-yansu-window-stress.sh` instead of invoking this
file directly.

Yansu often runs as a menu-bar app with no visible app windows. That is fine:
the stress loop uses other visible app windows to generate system activity that
Yansu observes. For unattended runs, the script will try to open Finder and
Calculator when there are not enough visible app windows.

Modes:

- `full`: switch windows and move windows
- `switch-only`: switch/raise windows without moving them
- `move-only`: move one selected window repeatedly

### `run-yansu-window-stress.sh`

Runs CPU sampling and the AppleScript stress loop together.

```bash
scripts/perf/yansu-activity-cpu/run-yansu-window-stress.sh Yansu 60 0.5 /tmp/yansu-full.csv full
```

Arguments:

1. App process name, default `Yansu`
2. Duration in seconds, default `60`
3. Sampling interval in seconds, default `0.5`
4. Output CSV path
5. Stress mode, default `full`

### `run-yansu-cpu-comparison.sh`

Runs a standard comparison suite and writes per-scenario CSVs plus a summary.

```bash
scripts/perf/yansu-activity-cpu/run-yansu-cpu-comparison.sh Yansu 60 0.5 10
```

Scenarios:

- `idle`
- `switch-only`
- `move-only`
- `full`

The script prints the output directory and writes `summary.csv` with averages,
percentiles, high-CPU sample counts, RSS, and action summary.

## Interpreting Results

The stress script intentionally creates unnatural window churn. Short CPU peaks
are expected when screenshots, AX parsing, PII masking, OCR, or memory cleanup
run. The signals to watch are:

- Sustained high average CPU during `full`
- High p90/p95 during `full`
- Repeated post-idle spikes after the stress loop stops
- RSS remaining inflated after snapshot-heavy sessions

For the activity CPU smoothing fix, the important validation was:

- The `full` stress loop stayed mostly in low double-digit CPU.
- The post-idle period no longer had repeated 200%+ CPU bursts.
- The OCR worker logged sampled processing for high-churn sessions, for example
  `sampling 6/20 snapshots for background OCR`.

## Useful Follow-Up Queries

Inspect recent snapshots:

```bash
sqlite3 ~/.yansu-agent/activity.db \
  "select datetime(timestamp/1000,'unixepoch','localtime') as ts, trigger_kind, session_id from activity_snapshots order by timestamp desc limit 30;"
```

Inspect recent sessions:

```bash
sqlite3 ~/.yansu-agent/activity.db \
  "select id, datetime(start_time/1000,'unixepoch','localtime'), datetime(end_time/1000,'unixepoch','localtime'), trigger_kind, event_count, snapshot_count, ocr_status from activity_sessions order by created_at desc limit 8;"
```

Check whether OCR sampled and embedded a session:

```bash
rg -n "sampling .*snapshots|\\[ocr\\] session .*processed|activity-embed" ~/.yansu-agent/yansu-gui.log
```
