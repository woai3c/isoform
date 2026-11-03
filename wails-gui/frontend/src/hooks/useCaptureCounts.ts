import { markLaunchPaint } from '@/lib/launch-perf';
import { useCallback, useEffect, useRef, useState } from "react";
import { type CaptureCounts } from "@/lib/wails-shim-app";
import { GetCaptureCounts, PageEventsOn as EventsOn, usePageQueryRefresh } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';

// Events after which the counts can change: a session was saved (screens,
// audio, typing and web calendar checks land then), a calendar was read, or
// observing was paused or resumed.
const REFRESH_EVENTS = ["activity:session-saved", "calendar:synced", "closed-eyes:changed"];

// Refreshes asked for within this long of each other run once, after the
// last: a session save and a calendar read often land together, and every
// count runs on the same database connection capture writes use.
const REFRESH_DEBOUNCE_MS = 500;

// While activity.db is still opening at launch the answer is notReady, and
// no event says when it opens, so the counts are asked for again this often
// until they are ready.
export const NOT_READY_RETRY_MS = 3000;

export interface CaptureCountsData {
    /** null until read, and when the read failed. */
    counts: CaptureCounts | null;
    loadFailed: boolean;
    refresh: () => void;
}

function localMidnight(now = new Date()): number {
    return new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
}

/**
 * What Isoform captured today and in the last seven days, for the card in
 * Settings > Observation and the line on Today. Refreshes on the events that
 * change the counts and on window focus, debounced.
 */
export function useCaptureCounts(enabled = true): CaptureCountsData {
    const [counts, setCounts] = useState<CaptureCounts | null>(() => pageQueryCache.peek('GetCaptureCounts', [localMidnight()]) ?? null);
    const [loadFailed, setLoadFailed] = useState(false);
    // Bumped by every refresh and on unmount; an answer whose version is
    // stale when it resolves is dropped, so overlapping refreshes cannot
    // leave the older answer on screen.
    const version = useRef(0);
    const retryTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

    const refresh = useCallback(() => {
        const current = ++version.current;
        clearTimeout(retryTimer.current);
        GetCaptureCounts(localMidnight())
            .then((next) => {
                if (current !== version.current) return;
                setCounts(next);
                if (!next.notReady) markLaunchPaint("counts_rendered");
                setLoadFailed(false);
                if (next.notReady) retryTimer.current = setTimeout(refresh, NOT_READY_RETRY_MS);
            })
            .catch(() => {
                if (current !== version.current) return;
                setCounts(null);
                setLoadFailed(true);
            });
    }, []);

    const debounceTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
    const refreshSoon = useCallback(() => {
        clearTimeout(debounceTimer.current);
        debounceTimer.current = setTimeout(refresh, REFRESH_DEBOUNCE_MS);
    }, [refresh]);
    usePageQueryRefresh(['GetCaptureCounts'], () => {
        const entry = pageQueryCache.state('GetCaptureCounts', [localMidnight()]);
        if (!entry || entry.invalid) refreshSoon();
        else refresh();
    }, enabled);

    useEffect(() => {
        if (!enabled) return;
        refresh();
        const onFocus = () => { pageQueryCache.invalidate(['GetCaptureCounts']); refreshSoon(); };
        const offs = REFRESH_EVENTS.map((name) => EventsOn(name, refreshSoon));
        window.addEventListener("focus", onFocus);
        return () => {
            version.current += 1;
            clearTimeout(debounceTimer.current);
            clearTimeout(retryTimer.current);
            offs.forEach((off) => off());
            window.removeEventListener("focus", onFocus);
        };
    }, [enabled, refresh, refreshSoon]);

    return { counts, loadFailed, refresh: () => { pageQueryCache.invalidate(['GetCaptureCounts']); return refresh(); } };
}
