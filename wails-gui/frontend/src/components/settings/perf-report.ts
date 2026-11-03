import { NativeClipboardSetText } from "../../../wailsjs/go/main/App";
import { ClipboardSetText } from "../../../wailsjs/runtime/runtime";
import { toast } from "sonner";

// ── Perf Analysis report ──
// Text report of a runtime snapshot, shared by the Developer tools App health
// page and the "Copy diagnostics" button on Help and about.

export function fmtDuration(totalSec: number): string {
    if (!Number.isFinite(totalSec) || totalSec <= 0) return '—';
    const d = Math.floor(totalSec / 86400);
    const h = Math.floor((totalSec % 86400) / 3600);
    const m = Math.floor((totalSec % 3600) / 60);
    const s = Math.floor(totalSec % 60);
    const parts: string[] = [];
    if (d) parts.push(`${d}d`);
    if (h) parts.push(`${h}h`);
    if (m) parts.push(`${m}m`);
    if (!d && !h && (s || !m)) parts.push(`${s}s`);
    return parts.join(' ') || '0s';
}

export function fmtMB(n: number | undefined): string {
    if (!n && n !== 0) return '—';
    if (n >= 1024) return `${(n / 1024).toFixed(2)} GB`;
    if (n >= 10) return `${n.toFixed(0)} MB`;
    return `${n.toFixed(1)} MB`;
}

export function fmtPct(n: number | undefined): string {
    if (n === undefined || n === null) return '—';
    return `${n.toFixed(1)}%`;
}

export function fmtMs(n: number | undefined): string {
    if (!n && n !== 0) return '—';
    if (n < 1) return `${(n * 1000).toFixed(0)}µs`;
    if (n < 1000) return `${n.toFixed(2)}ms`;
    return `${(n / 1000).toFixed(2)}s`;
}

export type PerfSnapshotShape = {
    timestamp: number;
    uptime_ms: number;
    version: string;
    go_version: string;
    platform: string;
    git_commit?: string;
    git_dirty?: boolean;
    build_time?: string;
    runtime: {
        goroutines: number;
        os_threads: number;
        cgo_calls_total: number;
        heap_in_use_mb: number;
        heap_sys_mb: number;
        heap_allocs_total_mb: number;
        gc_pause_last_ms: number;
        num_gc: number;
        rss_mb: number;
        vsz_mb: number;
        cpu_pct: number;
    };
    subprocesses: Array<{
        pid: number;
        ppid: number;
        pgid: number;
        kind: string;
        orphan: boolean;
        elapsed_seconds: number;
        rss_mb: number;
        cpu_pct: number;
        session_id?: string;
        model?: string;
        command: string;
    }>;
    activity_mon: {
        events_drained: number;
        wakeups: number;
        ring_peak_depth: number;
        ring_drops: number;
    };
    activity_pipeline?: {
        background_audio: {
            microphone: PerfBackgroundAudioSourceShape;
            system_audio: PerfBackgroundAudioSourceShape;
            whisper_in_flight: number;
            whisper_requests: number;
            whisper_errors: number;
            whisper_empty_transcripts: number;
            whisper_dropped_transcripts: number;
            whisper_accepted_transcripts: number;
            whisper_last_ms: number;
            whisper_p50_ms: number;
            whisper_p95_ms: number;
        };
        capture: {
            attempts: number;
            completed: number;
            skipped_backpressure: number;
            skipped_dedup: number;
            dropped_pii_busy: number;
            last_ms: number;
            p50_ms: number;
            p95_ms: number;
        };
        pii: {
            in_flight: number;
            launched: number;
            busy_drops: number;
            throttled?: number;
            last_ms: number;
            p50_ms: number;
            p95_ms: number;
            ready?: boolean;
            init_in_flight?: boolean;
            mode?: string;
            queued_frames?: number;
            queued_bytes?: number;
            draining?: boolean;
            retry_after_ms?: number;
        };
        visual: {
            checks: number;
            skipped: number;
            promoted: number;
            last_ms: number;
            p50_ms: number;
            p95_ms: number;
        };
    };
    pprof: { enabled: boolean; url?: string };
    warnings?: string[];
};

type PerfBackgroundAudioSourceShape = {
    queued_chunks: number;
    dropped_chunks: number;
    coalesced_chunks: number;
    queue_depth: number;
    queue_duration_ms: number;
};

const PERF_KIND_LABEL: Record<string, string> = {
    yansu: 'Isoform',
    'cli.js': 'cli.js',
    mcp: 'MCP server',
    whisper: 'Whisper',
};

const PERF_KIND_ORDER = ['yansu', 'cli.js', 'mcp', 'whisper'];

export function sortSubprocs(
    a: PerfSnapshotShape['subprocesses'][number],
    b: PerfSnapshotShape['subprocesses'][number],
): number {
    const ai = PERF_KIND_ORDER.indexOf(a.kind);
    const bi = PERF_KIND_ORDER.indexOf(b.kind);
    if (ai !== bi) return (ai < 0 ? 99 : ai) - (bi < 0 ? 99 : bi);
    return b.rss_mb - a.rss_mb;
}

function renderPerfReport(snap: PerfSnapshotShape): string {
    const lines: string[] = [];
    const ts = new Date(snap.timestamp).toISOString();
    lines.push('Isoform Perf Snapshot');
    lines.push('===================');
    lines.push(`Captured:  ${ts}`);
    lines.push(`Uptime:    ${fmtDuration(snap.uptime_ms / 1000)}`);
    lines.push(`Version:   ${snap.version} (${snap.go_version}, ${snap.platform})`);
    if (snap.git_commit || snap.build_time) {
        const commit = snap.git_commit || 'unknown';
        const dirty = snap.git_dirty ? ' (dirty)' : '';
        const built = snap.build_time ? `  built ${snap.build_time}` : '';
        lines.push(`Build:     ${commit}${dirty}${built}`);
    }
    lines.push('');
    lines.push('Runtime');
    lines.push('-------');
    lines.push(`  Goroutines:    ${snap.runtime.goroutines}`);
    if (snap.runtime.os_threads > 0) {
        lines.push(`  OS threads:    ${snap.runtime.os_threads}`);
    }
    lines.push(`  Cgo calls:     ${snap.runtime.cgo_calls_total.toLocaleString()} (cumulative)`);
    lines.push(`  Heap in-use:   ${fmtMB(snap.runtime.heap_in_use_mb)}`);
    lines.push(`  Heap sys:      ${fmtMB(snap.runtime.heap_sys_mb)}`);
    lines.push(`  Total allocs:  ${fmtMB(snap.runtime.heap_allocs_total_mb)} (cumulative)`);
    lines.push(`  GC count:      ${snap.runtime.num_gc} (last pause ${fmtMs(snap.runtime.gc_pause_last_ms)})`);
    lines.push(
        `  Process RSS:   ${fmtMB(snap.runtime.rss_mb)}  VSZ: ${fmtMB(snap.runtime.vsz_mb)}  CPU: ${fmtPct(snap.runtime.cpu_pct)}`,
    );
    lines.push('');
    // activity_mon is zero on non-darwin and when the monitor hasn't
    // been started; skip the section to avoid a block of zeros in that
    // case (the test is conservative: we only hide it when everything
    // is pristine zero, so a drop or a peak will always show up).
    const am = snap.activity_mon;
    const amHasSignal =
        am && (am.events_drained > 0 || am.wakeups > 0 || am.ring_peak_depth > 0 || am.ring_drops > 0);
    if (amHasSignal) {
        lines.push('Activity Monitor');
        lines.push('----------------');
        lines.push(`  Events drained:  ${am.events_drained.toLocaleString()}`);
        lines.push(`  Wakeups:         ${am.wakeups.toLocaleString()}`);
        lines.push(`  Ring peak depth: ${am.ring_peak_depth} / 512`);
        lines.push(`  Ring drops:      ${am.ring_drops}`);
        lines.push('');
    }
    const pipeline = snap.activity_pipeline;
    if (pipeline) {
        lines.push('Activity flow');
        lines.push('-------------');
        lines.push(
            `  Audio mic queue:    ${pipeline.background_audio.microphone.queue_depth} chunks / ${fmtMs(pipeline.background_audio.microphone.queue_duration_ms)}`,
        );
        lines.push(
            `  Audio system queue: ${pipeline.background_audio.system_audio.queue_depth} chunks / ${fmtMs(pipeline.background_audio.system_audio.queue_duration_ms)}`,
        );
        lines.push(
            `  Whisper:            in-flight ${pipeline.background_audio.whisper_in_flight}, requests ${pipeline.background_audio.whisper_requests}, errors ${pipeline.background_audio.whisper_errors}, p95 ${fmtMs(pipeline.background_audio.whisper_p95_ms)}`,
        );
        lines.push(
            `  Capture:            ${pipeline.capture.completed}/${pipeline.capture.attempts} completed, p95 ${fmtMs(pipeline.capture.p95_ms)}, PII busy drops ${pipeline.capture.dropped_pii_busy}`,
        );
        const piiMode = pipeline.pii.mode || (pipeline.pii.ready ? 'gliner' : 'unknown');
        const piiQueuedFrames = pipeline.pii.queued_frames ?? 0;
        const piiQueuedBytes = pipeline.pii.queued_bytes ?? 0;
        const piiRetry =
            pipeline.pii.retry_after_ms && pipeline.pii.retry_after_ms > 0
                ? `, retry in ${fmtMs(pipeline.pii.retry_after_ms)}`
                : '';
        lines.push(
            `  PII:                mode ${piiMode}, in-flight ${pipeline.pii.in_flight}, launched ${pipeline.pii.launched}, busy ${pipeline.pii.busy_drops}, throttled ${pipeline.pii.throttled ?? 0}, queued ${piiQueuedFrames} (${fmtMB(piiQueuedBytes / 1024 / 1024)}), p95 ${fmtMs(pipeline.pii.p95_ms)}${piiRetry}`,
        );
        lines.push(
            `  Visual:             checks ${pipeline.visual.checks}, skipped ${pipeline.visual.skipped}, promoted ${pipeline.visual.promoted}, p95 ${fmtMs(pipeline.visual.p95_ms)}`,
        );
        lines.push('');
    }
    lines.push(`Subprocesses (${snap.subprocesses.length})`);
    lines.push('------------');
    for (const sp of [...snap.subprocesses].sort(sortSubprocs)) {
        const label = (PERF_KIND_LABEL[sp.kind] ?? sp.kind) + (sp.orphan ? ' (orphan)' : '');
        const meta =
            sp.kind === 'cli.js' && sp.session_id
                ? `${sp.model || '?'} (${sp.session_id.slice(0, 8)})`
                : '—';
        lines.push(
            `  ${String(sp.pid).padEnd(7)} ${label.padEnd(20)} ${meta.padEnd(28)} ${fmtDuration(sp.elapsed_seconds).padStart(8)}  ${fmtMB(sp.rss_mb).padStart(10)}  ${fmtPct(sp.cpu_pct).padStart(7)}`,
        );
    }
    // Only surface pprof when it's actually listening. When disabled,
    // omit the line entirely — the env var name that enables it is a
    // developer detail that doesn't belong in a shared bug report and
    // isn't useful to a regular user reading the snapshot.
    if (snap.pprof.enabled && snap.pprof.url) {
        lines.push('');
        lines.push(`Pprof: ${snap.pprof.url}`);
    }
    if (snap.warnings?.length) {
        lines.push('');
        lines.push('Warnings');
        lines.push('--------');
        for (const w of snap.warnings) lines.push(`  - ${w}`);
    }
    return lines.join('\n');
}

// copyPerfReport formats a PerfSnapshot as text and copies it through the
// same three-tier fallback the Perf Analysis panel uses (native NSPasteboard
// → Wails pbcopy → DOM execCommand). Shared so the About-page "Copy
// diagnostics" button and the Perf panel's copy button behave identically.
export async function copyPerfReport(snap: PerfSnapshotShape, successMessage: string, copyFailedTpl?: (err: unknown) => string, copyFailedManual?: string): Promise<void> {
    const text = renderPerfReport(snap);

    // Native Wails mode: use our NSPasteboard bridge instead of the bundled
    // Wails runtime ClipboardSetText, which shells out to `pbcopy` and fails
    // silently inside a Finder-launched hardened-runtime app when the child
    // PATH doesn't include /usr/bin.
    const hasNativeRuntime =
        typeof window !== 'undefined' &&
        !!(window as unknown as { runtime?: { ClipboardSetText?: unknown } }).runtime?.ClipboardSetText;

    if (hasNativeRuntime) {
        try {
            await NativeClipboardSetText(text);
            toast.success(successMessage);
            return;
        } catch (err) {
            // If the cgo bridge errored out, try Wails's pbcopy path as a
            // second attempt before giving up.
            try {
                await ClipboardSetText(text);
                toast.success(successMessage);
                return;
            } catch {
                toast.error(copyFailedTpl ? copyFailedTpl(err) : `Copy failed: ${err}`);
                return;
            }
        }
    }

    // Browser-bridge mode: the shim's navigator.clipboard.writeText can
    // resolve to `false` inside some Chrome extension contexts, hence the
    // explicit boolean check and the textarea + execCommand fallback.
    try {
        const ok = await ClipboardSetText(text);
        if (ok) {
            toast.success(successMessage);
            return;
        }
    } catch {
        // swallow — fall through to the execCommand fallback below
    }

    try {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.focus();
        ta.select();
        const ok = document.execCommand('copy');
        document.body.removeChild(ta);
        if (ok) {
            toast.success(successMessage);
            return;
        }
        toast.error(copyFailedManual ?? 'Copy failed — select the report text manually.');
    } catch (err) {
        toast.error(copyFailedTpl ? copyFailedTpl(err) : `Copy failed: ${err}`);
    }
}
