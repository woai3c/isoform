import { useCallback, useEffect, useRef, useState } from 'react';
import type { TodayDayShape } from '@/lib/wails-shim-app';
import { GetTodayDayShape, todayDayShapeDate, usePageQueryRefresh } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { appActive, onAppShown } from '@/lib/app-activity';
import { onHoldSettingsChanged } from '@/lib/hold-settings-events';

/** Requested civil date is UI provenance, separate from the backend contract. */
export interface TodayDayShapeState {
    shape: TodayDayShape | null;
    dateISO: string;
    failed: boolean;
    lastUpdatedAt: number | null;
    retry: () => void;
}

function sameShape(a: TodayDayShape | null, b: TodayDayShape | null): boolean {
    return a === b || !!a && !!b && JSON.stringify(a) === JSON.stringify(b);
}

/** Cached calendar preview only. A successful same-day read is authoritative:
 * it replaces the drawing, including removals, empty days and disconnection.
 * Only a pending or failed read keeps the prior drawing; privacy invalidation
 * still removes it before the gated read. */
export function useTodayDayShape(enabled: boolean, briefDate?: string, evening = false): TodayDayShapeState {
    const dateISO = todayDayShapeDate(briefDate, evening);
    const [view, setView] = useState<{ shape: TodayDayShape | null; dateISO: string }>(() => ({
        shape: enabled ? pageQueryCache.peek<TodayDayShape>('GetTodayDayShape', [dateISO]) ?? null : null,
        dateISO,
    }));
    const [failed, setFailed] = useState(false);
    const [lastUpdatedAt, setLastUpdatedAt] = useState<number | null>(() => pageQueryCache.state('GetTodayDayShape', [dateISO])?.at || null);
    const version = useRef(0);
    const refresh = useCallback(() => {
        const current = ++version.current;
        const nextDateISO = todayDayShapeDate(briefDate, evening);
        const publish = (shape: TodayDayShape | null) => setView((prior) => {
            const retained = sameShape(prior.shape, shape) ? prior.shape : shape;
            return prior.shape === retained && prior.dateISO === nextDateISO ? prior : { shape: retained, dateISO: nextDateISO };
        });
        const cached = enabled ? pageQueryCache.peek<TodayDayShape>('GetTodayDayShape', [nextDateISO]) ?? null : null;
        publish(cached);
        if (!cached) setLastUpdatedAt(null);
        if (!enabled) { setFailed(false); return; }
        void GetTodayDayShape(nextDateISO).then((shape) => {
            if (current === version.current) {
                publish(shape);
                setLastUpdatedAt(pageQueryCache.state('GetTodayDayShape', [nextDateISO])?.at || Date.now());
                setFailed(false);
            }
        }, () => {
            if (current === version.current) setFailed(true);
        });
    }, [enabled, briefDate, evening]);

    // Hidden pages still discard private local state, without starting reads.
    usePageQueryRefresh(['GetTodayDayShape'], refresh, true,
        (args) => args[0] === todayDayShapeDate(briefDate, evening));
    useEffect(() => {
        refresh();
        if (!enabled) return;
        return () => { version.current++; };
    }, [enabled, refresh]);
    useEffect(() => onHoldSettingsChanged(() => pageQueryCache.invalidate(['GetTodayDayShape'], false), false), []);
    // A new day reads its own strip: once at local midnight while the window
    // is looked at, otherwise the moment it is shown again.
    const requested = useRef(dateISO);
    requested.current = view.dateISO;
    useEffect(() => {
        if (!enabled) return;
        const dayChanged = () => {
            if (todayDayShapeDate(briefDate, evening) !== requested.current) refresh();
        };
        let timer: number | undefined;
        const schedule = () => {
            const now = new Date();
            const midnight = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).getTime();
            timer = window.setTimeout(() => {
                if (appActive()) dayChanged();
                schedule();
            }, midnight - now.getTime() + 1000);
        };
        schedule();
        const off = onAppShown(dayChanged);
        return () => { window.clearTimeout(timer); off(); };
    }, [enabled, briefDate, evening, refresh]);
    const retry = useCallback(() => pageQueryCache.invalidate(['GetTodayDayShape'], false, [dateISO]), [dateISO]);
    return view.dateISO === dateISO ? { ...view, failed, lastUpdatedAt, retry } : {
        shape: enabled ? pageQueryCache.peek<TodayDayShape>('GetTodayDayShape', [dateISO]) ?? null : null,
        dateISO, failed: false, lastUpdatedAt: null, retry,
    };
}
