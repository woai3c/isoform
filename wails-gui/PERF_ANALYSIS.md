# Perf Analysis — operator guide

DevTools panel that surfaces Yansu's live resource footprint and the
entire subprocess family tree, with an opinionated "is anything
wrong?" check on top. Designed so an engineer or support responder
receiving a bug report can ask the user for one snapshot instead of
chasing pprof / ps / vmmap by hand.

Gated behind **Settings → Dev Tools → Perf Analysis**. Dev Tools
section only renders when `isDev || isStaging` is true; production
users flip staging via five rapid logo clicks or
`Cmd+Shift+Option+E`.

## What each section shows

| Section | Where the number comes from | What "normal" looks like |
|---|---|---|
| **Uptime / Version** | `recordAppStart()` in `app.go startup()` + `Version` var | purely informational |
| **Runtime** | `runtime.ReadMemStats`, `runtime.NumGoroutine`, `debug.ReadGCStats`, `ps -p $$ -o rss,vsz,%cpu` | goroutines ≈ 60 idle, heap in-use 10–50 MB, RSS 200–400 MB warm |
| **Subprocesses** | single `ps -A -o pid,ppid,pgid,etime,rss,%cpu,command` parse, classified by command substring | 1 × yansu, N × cli.js per active Claude Code CLI invocation, M × mcp per plugin the user's MCP servers expose. (Speech transcription used to spawn a `whisper-server` subprocess; sherpa-onnx now runs in-process so no separate row appears.) |
| **Pprof** | reads `YANSU_PPROF` env at snapshot time | "enabled" if the env var is set at launch (LSEnvironment or shell); the URL button opens it in the user's default browser |

Auto-refresh is every 3 s. `ps` calls dominate the cost; the whole
snapshot typically takes well under 200 ms.

## Classifier

`perfClassify` maps a `ps` row to one of `yansu / cli.js / mcp` by
command-line substring (the legacy `whisper` bucket is retained in the
metric struct but no longer matched, since sherpa-onnx is in-process).
MCP matches
(`@upstash/context7-mcp`, `mcp-science`, `gemini-mcp-tool`,
`claude-plugins-official`) are constrained to rows whose `pgid`
belongs to this process tree so siblings from other Claude Code
sessions on the machine aren't attributed here.

`Orphan` = `ppid == 1` **and** kind is one that should never be
launchd-parented (only `mcp` or `cli.js`). A row with `(orphan)` in
the kind column is a structurally dead process: its parent exited
without cleaning it up and it will sit there at whatever CPU % it
had until something sweeps it.

## Warning thresholds

Computed live from the snapshot, never hard-coded. `perfDeriveWarnings`
in `perf_analysis.go`.

| Warning | Formula | Rationale |
|---|---|---|
| MCP orphan | `any sp.Orphan && sp.Kind == "mcp"` | Structurally wrong regardless of load |
| Goroutine flood | `goroutines > 120` | 80 base + 40 scanner/pipeline headroom |
| RSS bloat | `rss_mb > 300 + cli.js*80 + 150 + gliner*800` | 300 baseline + 80/cli.js + 150 for activity monitor + GLiNER headroom when model PII is active |

Size-based warnings (RSS, goroutines) are **suppressed during the
first 3 minutes of uptime**. Cocoa/WebKit init, ONNX model load, PII
tokenizer and the Go runtime arena all land heap pages during startup
and rely on GC + MADV_FREE to unwind; peak is ~750 MB at 8 s, drops to
~460 MB by 1 m, settles under 300 MB by 5 m. Structural warnings
(orphans) run from the start.

## Copy Report

Single button. Writes a plain-text block:

```
Yansu Perf Snapshot
===================
Captured:  2026-04-18T15:45:32Z
Uptime:    1h 23m
Version:   0.1.33 (go1.24.3, darwin/arm64)

Runtime
-------
  Goroutines:    58
  Heap in-use:   18 MB
  Heap sys:      50 MB
  Total allocs:  34.9 GB (cumulative)
  GC count:      123 (last pause 0.12ms)
  Process RSS:   203 MB  VSZ: 1.2 GB  CPU: 12.3%

Subprocesses (4)
----------------
  73215  Yansu        —                   1h 23m    203 MB   12.3%
  73506  cli.js       haiku (327e8df5)    54m        77 MB    0.0%
  ...

Pprof: enabled (http://127.0.0.1:6060/debug/pprof/)

Warnings
--------
  - <structural warning if any>
```

Format is stable — consumers can pattern-match on section headers.

### How the copy actually works

Native Wails mode uses the macOS NSPasteboard bridge in
`clipboard_darwin.go (NativeClipboardSetText)`. The bundled Wails v2
`ClipboardSetText` shells out to `pbcopy`, which fails silently inside
a Finder-launched hardened-runtime app when the child PATH doesn't
include `/usr/bin` — so we reach Cocoa directly via cgo.

Browser-bridge mode uses `navigator.clipboard.writeText`, with a
`document.execCommand('copy')` fallback on a hidden textarea for
Chrome-extension contexts where the async API is blocked.

## Triage playbook

**"My Yansu is slow"**

1. Open Perf Analysis. Read the warnings banner first.
2. Click Copy Report, paste into the bug ticket.
3. If the banner flags a structural issue, that's almost always the
   root cause — jump to the matching entry below.

**"MCP orphan" (warning)**

Amber rows in the Subprocesses table with `(orphan)` label, `ppid=1`.
If the count persists across a refresh, something upstream spawned an
MCP server and died without cleanup. Capture the Copy Report and open
an issue.

**"Goroutine flood" or "RSS bloat" after 3 min uptime**

Enable pprof (`YANSU_PPROF=1` in `LSEnvironment` or shell at launch),
click the "Open pprof" button, grab:
- `goroutine?debug=2` for a full dump
- `heap` for in-use decomposition
- `profile?seconds=30` for a CPU flame graph

Attach all three to the bug.

**"577 MB at 1m uptime"** — not a real problem. Startup descent. Wait
until uptime > 3 min; this warning is suppressed in that window.

## Known caveats

- Windows: `ps` isn't available, so the subprocess table and OS-level
  RSS/VSZ/CPU fields are empty in the copied report. `Process RSS: 0.0 MB`
  and `CPU: 0.0%` on Windows mean "not sampled", not "free". Go runtime
  numbers (goroutines, heap, GC) and activity pipeline metrics still work.
  Use Task Manager for live CPU until Windows-specific process sampling is
  implemented.
- Windows PII triage: after GLiNER became fp32-only, the important signals are
  `PII p95`, `busy drops`, `in-flight`, `heap sys`, and watchdog stability.
  A healthy run should not resemble the old failure mode where ORT used all
  physical cores and PII p95 reached tens of seconds.
- Multiple Yansu instances on the same machine: only rows whose pgid
  belongs to this process end up in the MCP bucket; processes from
  other instances show up with their own Yansu parent in the table,
  which is cosmetically confusing but not broken.

## Files

- `wails-gui/perf_analysis.go` — snapshot builder, classifier,
  warnings
- `wails-gui/clipboard_darwin.go` + `clipboard_other.go` — native
  clipboard bridge
- `wails-gui/frontend/src/components/settings-page.tsx` —
  `PerfAnalysisSettings` component (search for the function name)
