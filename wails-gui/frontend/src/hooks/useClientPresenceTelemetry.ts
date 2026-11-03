import { useEffect, useRef } from 'react';
import posthog from 'posthog-js';

export type ClientPresenceState = 'foreground' | 'background';
export type PresenceFlushReason = 'startup' | 'interval' | 'state_change' | 'midnight' | 'logout' | 'unload';

const EVENT_NAME = 'client.presence.duration_recorded';
const STARTUP_FLUSH_DELAY_MS = 5_000;
const FLUSH_INTERVAL_MS = 60_000;
const SLEEP_GAP_THRESHOLD_MS = 10 * 60_000;
const MIN_DURATION_MS = 1_000;

interface PresenceSlice {
    startMs: number;
    endMs: number;
    localDate: string;
}

interface CapturePresenceSliceParams extends PresenceSlice {
    state: ClientPresenceState;
    reason: PresenceFlushReason;
    sequence: number;
    sessionId: string;
    gapDetected: boolean;
}

function pad2(value: number): string {
    return String(value).padStart(2, '0');
}

export function localDateKey(ms: number): string {
    const date = new Date(ms);
    return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`;
}

function nextLocalMidnightMs(ms: number): number {
    const date = new Date(ms);
    return new Date(date.getFullYear(), date.getMonth(), date.getDate() + 1).getTime();
}

export function splitPresenceSlices(startMs: number, endMs: number): PresenceSlice[] {
    if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || endMs <= startMs) {
        return [];
    }

    const slices: PresenceSlice[] = [];
    let cursor = startMs;

    while (cursor < endMs) {
        const sliceEnd = Math.min(endMs, nextLocalMidnightMs(cursor));
        slices.push({
            startMs: cursor,
            endMs: sliceEnd,
            localDate: localDateKey(cursor),
        });
        cursor = sliceEnd;
    }

    return slices;
}

export function getClientPresenceState(): ClientPresenceState {
    if (typeof document === 'undefined') {
        return 'background';
    }

    if (document.visibilityState === 'visible' && document.hasFocus()) {
        return 'foreground';
    }

    return 'background';
}

function timezoneName(): string {
    try {
        return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
    } catch {
        return '';
    }
}

function createSessionId(): string {
    if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
        return crypto.randomUUID();
    }
    return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

function isPostHogLoaded(): boolean {
    return Boolean((posthog as { __loaded?: boolean }).__loaded);
}

function capturePresenceSlice({
    state,
    reason,
    startMs,
    endMs,
    localDate,
    sequence,
    sessionId,
    gapDetected,
}: CapturePresenceSliceParams) {
    const durationMs = endMs - startMs;
    if (durationMs < MIN_DURATION_MS) {
        return;
    }

    posthog.capture(EVENT_NAME, {
        client_type: 'desktop',
        source: 'wails-gui-frontend',
        state,
        duration_ms: durationMs,
        local_date: localDate,
        timezone: timezoneName(),
        slice_started_at: new Date(startMs).toISOString(),
        slice_ended_at: new Date(endMs).toISOString(),
        flush_reason: reason,
        gap_detected: gapDetected,
        $insert_id: `client-presence-${sessionId}-${sequence}-${state}-${startMs}-${endMs}`,
    });
}

export function useClientPresenceTelemetry(isAuthenticated: boolean, isTelemetryReady = true) {
    const stateRef = useRef<ClientPresenceState>(getClientPresenceState());
    const startedAtRef = useRef(Date.now());
    const sessionIdRef = useRef(createSessionId());
    const sequenceRef = useRef(0);

    useEffect(() => {
        if (!isAuthenticated || !isTelemetryReady) {
            return;
        }

        stateRef.current = getClientPresenceState();
        startedAtRef.current = Date.now();

        const flush = (reason: PresenceFlushReason) => {
            if (!isPostHogLoaded()) {
                return;
            }

            const now = Date.now();
            const startedAt = startedAtRef.current;
            const elapsedMs = now - startedAt;
            const gapDetected = elapsedMs > SLEEP_GAP_THRESHOLD_MS;
            const endMs = gapDetected ? startedAt + FLUSH_INTERVAL_MS : now;

            for (const slice of splitPresenceSlices(startedAt, endMs)) {
                const sequence = sequenceRef.current;
                sequenceRef.current += 1;
                capturePresenceSlice({
                    ...slice,
                    state: stateRef.current,
                    reason,
                    sequence,
                    sessionId: sessionIdRef.current,
                    gapDetected,
                });
            }

            startedAtRef.current = now;
        };

        const syncState = () => {
            const nextState = getClientPresenceState();
            if (nextState === stateRef.current) {
                return;
            }

            flush('state_change');
            stateRef.current = nextState;
            startedAtRef.current = Date.now();
        };

        const startupTimeout = window.setTimeout(() => {
            flush('startup');
        }, STARTUP_FLUSH_DELAY_MS);
        const flushInterval = window.setInterval(() => {
            flush('interval');
        }, FLUSH_INTERVAL_MS);
        const midnightInterval = window.setInterval(() => {
            if (localDateKey(startedAtRef.current) !== localDateKey(Date.now())) {
                flush('midnight');
            }
        }, 30_000);
        const flushBeforeUnload = () => flush('unload');

        window.addEventListener('focus', syncState);
        window.addEventListener('blur', syncState);
        window.addEventListener('pagehide', flushBeforeUnload);
        window.addEventListener('beforeunload', flushBeforeUnload);
        document.addEventListener('visibilitychange', syncState);

        syncState();

        return () => {
            window.clearTimeout(startupTimeout);
            window.clearInterval(flushInterval);
            window.clearInterval(midnightInterval);
            window.removeEventListener('focus', syncState);
            window.removeEventListener('blur', syncState);
            window.removeEventListener('pagehide', flushBeforeUnload);
            window.removeEventListener('beforeunload', flushBeforeUnload);
            document.removeEventListener('visibilitychange', syncState);
            flush('logout');
        };
    }, [isAuthenticated, isTelemetryReady]);
}
