package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"wails-gui/internal/platform/activitymon"
)

// PerfSnapshot is a point-in-time view of Yansu's resource footprint and
// subprocess family. Surfaced to the DevTools "Perf Analysis" panel so
// users hitting performance issues can inspect (and copy out) the same
// diagnostics we'd ask them for in a bug report.
type PerfSnapshot struct {
	Timestamp int64 `json:"timestamp"` // unix ms
	UptimeMs  int64 `json:"uptime_ms"` // ms since app startup

	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"` // "darwin/arm64" etc.

	// Build provenance — populated via ldflags in release builds, via
	// runtime/debug VCS metadata in dev builds. Lets a recipient tell at a
	// glance whether a reported snapshot comes from pre- or post-fix code
	// without round-tripping through the user.
	GitCommit string `json:"git_commit,omitempty"`
	GitDirty  bool   `json:"git_dirty,omitempty"`
	BuildTime string `json:"build_time,omitempty"`

	Runtime          PerfRuntimeStats          `json:"runtime"`
	Subprocesses     []PerfSubprocInfo         `json:"subprocesses"`
	ActivityMon      PerfActivityMonStats      `json:"activity_mon"`
	ActivityPipeline PerfActivityPipelineStats `json:"activity_pipeline"`
	Pprof            PerfPprofInfo             `json:"pprof"`

	// Warnings are human-readable strings we derive from the numbers above,
	// so the panel can highlight likely problems (orphan cli.js, leaking
	// goroutines, etc.) without the user having to know the thresholds.
	Warnings []string `json:"warnings,omitempty"`
}

// PerfRuntimeStats captures Go runtime + OS-level memory numbers for the
// Yansu main process.
type PerfRuntimeStats struct {
	Goroutines        int     `json:"goroutines"`
	OSThreads         int     `json:"os_threads"`      // from `ps -M`; 0 on windows
	CgoCallsTotal     int64   `json:"cgo_calls_total"` // cumulative, delta-per-sec is a direct CGO-churn signal
	HeapInUseMB       float64 `json:"heap_in_use_mb"`
	HeapSysMB         float64 `json:"heap_sys_mb"`
	HeapAllocsTotalMB float64 `json:"heap_allocs_total_mb"` // cumulative
	GCPauseLastMs     float64 `json:"gc_pause_last_ms"`
	NumGC             uint32  `json:"num_gc"`
	RSSmb             float64 `json:"rss_mb"`  // ps rss of main process
	VSZmb             float64 `json:"vsz_mb"`  // virtual — mostly informational
	CPUPercent        float64 `json:"cpu_pct"` // ps %cpu of main (instantaneous)
}

// PerfActivityMonStats exposes the event-pipeline counters from the
// darwin (and, in future, windows) activity monitor. Three things to
// look for in a problem report:
//   - ring_drops > 0: producer is outrunning the Go drain goroutine
//   - ring_peak_depth near MAX_EVENT_QUEUE: same, about to drop
//   - wakeups / events_drained << 1: events are being drained in big
//     batches, which is healthy (the self-pipe coalesces bursts); if
//     the ratio is ~1:1 and the absolute rate is very high, something
//     upstream is generating events at an unusual pace
type PerfActivityMonStats struct {
	EventsDrained uint64 `json:"events_drained"`
	Wakeups       uint64 `json:"wakeups"`
	RingPeakDepth int    `json:"ring_peak_depth"`
	RingDrops     uint64 `json:"ring_drops"`
}

type PerfActivityPipelineStats struct {
	BackgroundAudio PerfBackgroundAudioStats `json:"background_audio"`
	Capture         PerfCapturePipelineStats `json:"capture"`
	PII             PerfPIIPipelineStats     `json:"pii"`
	Visual          PerfVisualPipelineStats  `json:"visual"`
}

type PerfBackgroundAudioStats struct {
	Microphone                 PerfBackgroundAudioSourceStats `json:"microphone"`
	SystemAudio                PerfBackgroundAudioSourceStats `json:"system_audio"`
	WhisperInFlight            int64                          `json:"whisper_in_flight"`
	WhisperRequests            uint64                         `json:"whisper_requests"`
	WhisperErrors              uint64                         `json:"whisper_errors"`
	WhisperEmptyTranscripts    uint64                         `json:"whisper_empty_transcripts"`
	WhisperDroppedTranscripts  uint64                         `json:"whisper_dropped_transcripts"`
	WhisperAcceptedTranscripts uint64                         `json:"whisper_accepted_transcripts"`
	WhisperLastMs              float64                        `json:"whisper_last_ms"`
	WhisperP50Ms               float64                        `json:"whisper_p50_ms"`
	WhisperP95Ms               float64                        `json:"whisper_p95_ms"`
}

type PerfBackgroundAudioSourceStats struct {
	QueuedChunks    uint64 `json:"queued_chunks"`
	DroppedChunks   uint64 `json:"dropped_chunks"`
	CoalescedChunks uint64 `json:"coalesced_chunks"`
	QueueDepth      int    `json:"queue_depth"`
	QueueDurationMs int64  `json:"queue_duration_ms"`
}

type PerfCapturePipelineStats struct {
	Attempts            uint64  `json:"attempts"`
	Completed           uint64  `json:"completed"`
	SkippedBackpressure uint64  `json:"skipped_backpressure"`
	SkippedDedup        uint64  `json:"skipped_dedup"`
	SkippedSelfFocus    uint64  `json:"skipped_self_focus"`
	SkippedFocusChanged uint64  `json:"skipped_focus_changed"`
	DroppedPIIBusy      uint64  `json:"dropped_pii_busy"`
	LastMs              float64 `json:"last_ms"`
	P50Ms               float64 `json:"p50_ms"`
	P95Ms               float64 `json:"p95_ms"`
}

type PerfPIIPipelineStats struct {
	InFlight     int64   `json:"in_flight"`
	Launched     uint64  `json:"launched"`
	BusyDrops    uint64  `json:"busy_drops"`
	Throttled    uint64  `json:"throttled"`
	LastMs       float64 `json:"last_ms"`
	P50Ms        float64 `json:"p50_ms"`
	P95Ms        float64 `json:"p95_ms"`
	Ready        bool    `json:"ready"`
	InitInFlight bool    `json:"init_in_flight"`
	Mode         string  `json:"mode"`
	QueuedFrames int     `json:"queued_frames"`
	QueuedBytes  int64   `json:"queued_bytes"`
	Draining     bool    `json:"draining"`
	RetryAfterMs int64   `json:"retry_after_ms,omitempty"`
}

type PerfVisualPipelineStats struct {
	Checks   uint64  `json:"checks"`
	Skipped  uint64  `json:"skipped"`
	Promoted uint64  `json:"promoted"`
	LastMs   float64 `json:"last_ms"`
	P50Ms    float64 `json:"p50_ms"`
	P95Ms    float64 `json:"p95_ms"`
}

// PerfSubprocInfo is one row in the subprocess family table.
type PerfSubprocInfo struct {
	PID            int     `json:"pid"`
	PPID           int     `json:"ppid"`
	PGID           int     `json:"pgid"`
	Kind           string  `json:"kind"`   // cli.js, mcp, whisper
	Orphan         bool    `json:"orphan"` // ppid == 1 (reparented to launchd); only non-noise for mcp / cli.js
	ElapsedSeconds int64   `json:"elapsed_seconds"`
	RSSmb          float64 `json:"rss_mb"`
	CPUPercent     float64 `json:"cpu_pct"`
	SessionID      string  `json:"session_id,omitempty"` // cli.js only
	Model          string  `json:"model,omitempty"`      // cli.js only
	Command        string  `json:"command"`              // first ~120 chars
}

// PerfPprofInfo tells the panel whether pprof is reachable so it can
// render a click-to-open link; no use making the user guess.
type PerfPprofInfo struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
}

// appStart records when startup() ran so we can report uptime without an
// extra plumbing pass through the App struct. Set by startup().
var appStart atomic.Int64

func recordAppStart() {
	appStart.CompareAndSwap(0, time.Now().UnixMilli())
}

// GetPerfSnapshot builds a fresh PerfSnapshot. The whole call should
// complete well under 200 ms on a normal machine; most of the cost is
// two ps invocations.
func (a *App) GetPerfSnapshot() (perfValue PerfSnapshot) {
	defer logPageBinding("GetPerfSnapshot", time.Now(), &perfValue)
	snap := PerfSnapshot{
		Timestamp: time.Now().UnixMilli(),
		Version:   Version,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if start := appStart.Load(); start > 0 {
		snap.UptimeMs = snap.Timestamp - start
	}

	snap.GitCommit, snap.GitDirty, snap.BuildTime = readBuildProvenance()
	snap.Runtime = perfRuntimeStats()
	// activitymon.GetStats is cheap (two atomic loads + a brief C mutex);
	// safe to call even when the monitor hasn't been started.
	amStats := activitymon.GetStats()
	snap.ActivityMon = PerfActivityMonStats{
		EventsDrained: amStats.EventsDrained,
		Wakeups:       amStats.Wakeups,
		RingPeakDepth: amStats.RingPeakDepth,
		RingDrops:     amStats.RingDrops,
	}
	snap.ActivityPipeline = activityPipelineMetrics.snapshot()
	a.fillPIIPerfStats(&snap.ActivityPipeline.PII)
	// Pgid membership is the only reliable "this subprocess belongs to *our*
	// Yansu" filter — MCP servers from other Claude Code sessions on the
	// same machine carry identical command lines and would otherwise get
	// lumped in (see perfClassify).
	ourPgids := map[int]bool{os.Getpid(): true}
	snap.Subprocesses = perfCollectSubprocesses(ourPgids)
	snap.Pprof = perfPprofInfo()
	snap.Warnings = perfDeriveWarnings(snap)

	return snap
}

// readBuildProvenance resolves the commit/dirty/time triple. Release builds
// inject these through -ldflags (see wails-gui/scripts/build-wails.sh); raw
// `go build` and `wails dev` fall through to runtime/debug VCS info which Go
// populates automatically since 1.18. Either channel is sufficient on its
// own, but having both means dev binaries never show "unknown" just because
// the build wasn't wrapped by the script.
func readBuildProvenance() (commit string, dirty bool, buildTime string) {
	commit = GitCommit
	buildTime = BuildTime
	if GitDirty != "" {
		dirty = GitDirty == "true"
	}

	if commit != "" && buildTime != "" && GitDirty != "" {
		return
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if commit == "" && len(s.Value) > 0 {
				if len(s.Value) >= 12 {
					commit = s.Value[:12]
				} else {
					commit = s.Value
				}
			}
		case "vcs.modified":
			if GitDirty == "" {
				dirty = s.Value == "true"
			}
		case "vcs.time":
			if buildTime == "" {
				buildTime = s.Value
			}
		}
	}
	return
}

func perfRuntimeStats() PerfRuntimeStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	var gc debug.GCStats
	debug.ReadGCStats(&gc)
	var lastPauseMs float64
	if len(gc.Pause) > 0 {
		lastPauseMs = float64(gc.Pause[0]) / float64(time.Millisecond)
	}

	stats := PerfRuntimeStats{
		Goroutines:        runtime.NumGoroutine(),
		OSThreads:         psSelfThreadCount(),
		CgoCallsTotal:     runtime.NumCgoCall(),
		HeapInUseMB:       bytesToMB(m.HeapInuse),
		HeapSysMB:         bytesToMB(m.HeapSys),
		HeapAllocsTotalMB: bytesToMB(m.TotalAlloc),
		GCPauseLastMs:     lastPauseMs,
		NumGC:             m.NumGC,
	}

	// RSS / VSZ / CPU via ps (one row). Swallow errors — partial snapshot
	// is still useful.
	rss, vsz, cpu := psSelfStats()
	stats.RSSmb = rss
	stats.VSZmb = vsz
	stats.CPUPercent = cpu
	return stats
}

// psSelfThreadCount returns the OS thread count of this process via
// `ps -M`. The key signal this unlocks: a goroutine count that's much
// lower than the thread count (seen in today's self-pipe rewrite: 67 vs
// 190) points at CGO call sites pinning threads or a GCD-backed library
// spawning its own pool, neither of which NumGoroutine reveals. Skipped
// on windows and on error (`ps -M` isn't available / allowed there).
func psSelfThreadCount() int {
	if runtime.GOOS == "windows" {
		return 0
	}
	out, err := exec.Command("ps", "-M", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	// `ps -M` prints one header line followed by one line per thread.
	// Counting newlines is sufficient since the header doesn't end with
	// a thread.
	n := strings.Count(string(out), "\n") - 1
	if n < 0 {
		return 0
	}
	return n
}

// psSelfStats queries `ps` for this process. Returns (rssMB, vszMB, cpuPct).
// Skipped on Windows where `ps` isn't available — the panel falls back to
// Go runtime numbers only on that platform.
func psSelfStats() (float64, float64, float64) {
	if runtime.GOOS == "windows" {
		return 0, 0, 0
	}
	pid := os.Getpid()
	out, err := exec.Command("ps", "-o", "rss=,vsz=,%cpu=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(out))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	rssKB, _ := strconv.ParseFloat(fields[0], 64)
	vszKB, _ := strconv.ParseFloat(fields[1], 64)
	cpu, _ := strconv.ParseFloat(fields[2], 64)
	return rssKB / 1024.0, vszKB / 1024.0, cpu
}

// perfCollectSubprocesses enumerates the Yansu family and tags each row
// with a user-facing "kind" so the UI can group them. ourPgids gates the
// MCP classifier: a process whose command line looks like an MCP server
// but whose pgid isn't in ourPgids almost certainly belongs to a different
// Claude Code session on the same machine and shouldn't be counted here.
func perfCollectSubprocesses(ourPgids map[int]bool) []PerfSubprocInfo {
	if runtime.GOOS == "windows" {
		return []PerfSubprocInfo{} // `ps` flags differ; Windows users rarely hit this panel.
	}
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,pgid=,etime=,rss=,%cpu=,command=").Output()
	if err != nil {
		return []PerfSubprocInfo{}
	}

	selfPid := os.Getpid()
	results := []PerfSubprocInfo{}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimLeft(line, " ")
		if line == "" {
			continue
		}
		// Expect 7 columns: pid, ppid, pgid, etime, rss, %cpu, command (rest).
		f := strings.Fields(line)
		if len(f) < 7 {
			continue
		}
		fields := []string{f[0], f[1], f[2], f[3], f[4], f[5], strings.Join(f[6:], " ")}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		pgid, _ := strconv.Atoi(fields[2])
		etime := fields[3]
		rssKB, _ := strconv.ParseFloat(fields[4], 64)
		cpu, _ := strconv.ParseFloat(fields[5], 64)
		command := strings.TrimSpace(fields[6])

		kind := perfClassify(pid, selfPid, pgid, ourPgids, command)
		if kind == "" {
			continue
		}

		info := PerfSubprocInfo{
			PID:  pid,
			PPID: ppid,
			PGID: pgid,
			Kind: kind,
			// Orphan (reparented to launchd) is only diagnostic for kinds
			// that are spawned by cli.js / the bundle. The main process,
			// whisper-server, and the bundle itself legitimately have
			// ppid=1 when Finder launches them.
			Orphan:         ppid == 1 && (kind == "mcp" || kind == "cli.js"),
			ElapsedSeconds: parseEtime(etime),
			RSSmb:          rssKB / 1024.0,
			CPUPercent:     cpu,
			Command:        truncateCmd(command, 160),
		}
		if kind == "cli.js" {
			info.SessionID = extractSessionIDFromCmdLine(command)
			info.Model = extractModelFromCmdLine(command)
		}
		results = append(results, info)
	}

	return results
}

// perfClassify decides whether a process belongs to the Yansu family and
// which bucket to put it in. Returns "" for unrelated processes.
//
// The MCP classifier has an extra pgid gate: command-line patterns like
// `@upstash/context7-mcp` match ANY Claude Code session on the machine,
// so we only mark a process as our MCP when its pgid is one our runner
// has spawned (current bundle pgid, or any historical bundle pgid we
// tracked in seenPgids). Without this, a user running other Claude Code
// sessions sees their siblings' MCP servers reported — and warned about —
// under this Yansu's roof.
func perfClassify(pid, selfPid, pgid int, ourPgids map[int]bool, command string) string {
	// `ps -o command=` prints argv joined with single spaces and does NOT
	// escape spaces inside the executable path. Splitting on the first
	// space would drop the tail of any install path under `/Users/<First
	// Last>/...`. Instead we match on the Yansu binary suffix anchored by
	// `.app/` (so it's always a file-path position) and require it to end
	// at a space or end-of-string (so a shell wrapper that just mentions
	// the path in the middle of its arguments doesn't match). Anchoring on
	// `.app/...` rather than `/Yansu.app/...` lets development flavors
	// match too; the inner executable is always
	// named "Yansu" regardless of the bundle name.
	const yansuBinSuffix = ".app/Contents/MacOS/Yansu"
	isYansuBinary := false
	if i := strings.Index(command, yansuBinSuffix); i >= 0 {
		end := i + len(yansuBinSuffix)
		isYansuBinary = end == len(command) || command[end] == ' '
	}

	switch {
	case pid == selfPid:
		return "yansu"
	case isYansuBinary:
		return "yansu"
	case strings.Contains(command, "claude-agent-sdk/cli.js"):
		return "cli.js"
	case strings.Contains(command, "whisper/bin/whisper-server") ||
		strings.Contains(command, "whisper-bundle/bin/whisper"):
		return "whisper"
	case strings.Contains(command, "claude-plugins-official") ||
		strings.Contains(command, "@upstash/context7-mcp") ||
		strings.Contains(command, "mcp-science") ||
		strings.Contains(command, "gemini-mcp-tool"):
		if ourPgids[pgid] {
			return "mcp"
		}
	}
	return ""
}

// parseEtime handles ps(1)'s elapsed-time format: "MM:SS", "HH:MM:SS",
// or "D-HH:MM:SS". Returns seconds.
func parseEtime(s string) int64 {
	if s == "" {
		return 0
	}
	days := int64(0)
	if idx := strings.Index(s, "-"); idx > 0 {
		if d, err := strconv.ParseInt(s[:idx], 10, 64); err == nil {
			days = d
		}
		s = s[idx+1:]
	}
	parts := strings.Split(s, ":")
	var h, m, sec int64
	switch len(parts) {
	case 3:
		h, _ = strconv.ParseInt(parts[0], 10, 64)
		m, _ = strconv.ParseInt(parts[1], 10, 64)
		sec, _ = strconv.ParseInt(parts[2], 10, 64)
	case 2:
		m, _ = strconv.ParseInt(parts[0], 10, 64)
		sec, _ = strconv.ParseInt(parts[1], 10, 64)
	default:
		return 0
	}
	return days*86400 + h*3600 + m*60 + sec
}

var modelArgRe = regexp.MustCompile(`--model\s+([A-Za-z0-9._-]+)`)
var sessionIDArgRe = regexp.MustCompile(`--session-id\s+([0-9a-fA-F-]{36})`)

func extractModelFromCmdLine(cmdLine string) string {
	if m := modelArgRe.FindStringSubmatch(cmdLine); len(m) > 1 {
		return m[1]
	}
	return ""
}

func extractSessionIDFromCmdLine(cmdLine string) string {
	if m := sessionIDArgRe.FindStringSubmatch(cmdLine); len(m) > 1 {
		return m[1]
	}
	return ""
}

func truncateCmd(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func bytesToMB(b uint64) float64 {
	return float64(b) / (1024.0 * 1024.0)
}

// perfPprofInfo reports whether YANSU_PPROF is set at launch so the panel
// can render an "open pprof" link only when the endpoint actually exists.
func perfPprofInfo() PerfPprofInfo {
	v := os.Getenv("YANSU_PPROF")
	if v == "" {
		return PerfPprofInfo{Enabled: false}
	}
	port := "6060"
	if v != "1" {
		port = v
	}
	return PerfPprofInfo{Enabled: true, URL: "http://127.0.0.1:" + port + "/debug/pprof/"}
}

// perfDeriveWarnings translates raw numbers into plain-language flags the
// panel highlights. A user with 8 open chats legitimately has 8 cli.js;
// that's not a leak. What IS a leak is MCP subprocesses with no live
// parent, or goroutines in excess of what active scanners + sessions can
// explain.
func perfDeriveWarnings(s PerfSnapshot) []string {
	var w []string

	// Cold start (first 3 min) shoulders a lot of transient allocation
	// from framework init — skip size-based checks there. Structural
	// checks (orphans) still run regardless.
	coldStart := s.UptimeMs > 0 && s.UptimeMs < 3*60*1000

	mcpOrphan := 0
	for _, sp := range s.Subprocesses {
		if sp.Kind == "mcp" && sp.Orphan {
			mcpOrphan++
		}
	}

	// MCP orphans are structurally wrong regardless of load: ppid == 1
	// means the cli.js parent already exited.
	if mcpOrphan >= 1 {
		w = append(w, fmt.Sprintf(
			"%d orphan MCP subprocess(es) — cli.js parent already exited. Reaper sweeps every 60 s; a persistent count means pgid doesn't match any seen bundle.",
			mcpOrphan))
	}

	// Goroutines: base 80, plus headroom for pipelines/scanners.
	goroLimit := 80 + 40
	if !coldStart && s.Runtime.Goroutines > goroLimit {
		w = append(w, fmt.Sprintf(
			"%d goroutines vs. expected limit %d (80 base + 8/retained session + 40 pipeline headroom). Check for a stuck scanner or a triager loop that never returns.",
			s.Runtime.Goroutines, goroLimit))
	}

	// OS threads vs goroutines: a ratio materially above 1.5 after cold
	// start means something is pinning threads outside the Go scheduler
	// — typically a synchronous CGO call (Vision, CoreAudio) or a
	// library that spawns its own pthread/GCD pool. This was the
	// smoking gun in the self-pipe rewrite (67 goroutines vs 190
	// threads). We allow extra slack for Wails's WebKit + a few helper
	// pools before flagging.
	if !coldStart && s.Runtime.OSThreads > 0 && s.Runtime.Goroutines > 0 {
		threadSlack := s.Runtime.Goroutines + 60 // WebKit + cgo helpers baseline
		if s.Runtime.OSThreads > threadSlack {
			w = append(w, fmt.Sprintf(
				"%d OS threads vs %d goroutines (headroom %d) — likely CGO / GCD pool pinning threads outside the Go scheduler. `samply` or pprof threadcreate will show which call site.",
				s.Runtime.OSThreads, s.Runtime.Goroutines, threadSlack))
		}
	}

	// Activity monitor ring: drops are always a bug. Peak near capacity
	// is pre-drop warning territory. These only meaningfully fire on
	// darwin (stub on non-darwin reports zeros).
	if s.ActivityMon.RingDrops > 0 {
		w = append(w, fmt.Sprintf(
			"activity monitor dropped %d events (ring full) — drain goroutine is falling behind producer bursts.",
			s.ActivityMon.RingDrops))
	} else if s.ActivityMon.RingPeakDepth >= 400 {
		// MAX_EVENT_QUEUE is 512; 400 is "about to drop".
		w = append(w, fmt.Sprintf(
			"activity monitor ring peak depth %d/512 — approaching capacity, watch for drops.",
			s.ActivityMon.RingPeakDepth))
	}

	// RSS: baseline + activity monitor overhead + optional GLiNER model
	// headroom. coldStart above is how we suppress the transient post-launch
	// spike (~750 → 300 MB over the first few minutes).
	cliJsCount := 0
	for _, sp := range s.Subprocesses {
		if sp.Kind == "cli.js" {
			cliJsCount++
		}
	}
	piiModelHeadroom := 0.0
	if s.ActivityPipeline.PII.Mode == "gliner" {
		piiModelHeadroom = 800.0
	}
	rssLimit := 300.0 + float64(cliJsCount)*80.0 + 150.0 + piiModelHeadroom
	if !coldStart && s.Runtime.RSSmb > rssLimit {
		glinerDesc := ""
		if piiModelHeadroom > 0 {
			glinerDesc = fmt.Sprintf(" + %.0f GLiNER", piiModelHeadroom)
		}
		w = append(w, fmt.Sprintf(
			"Main process RSS %.0f MB vs. expected ≤ %.0f MB (300 base + 80/cli.js + 150 activity monitor%s). Check vmmap for cgo retention if this keeps growing.",
			s.Runtime.RSSmb, rssLimit, glinerDesc))
	}

	return w
}
