import { GetEngagementWeekSummary, GetMobileBriefHealth, usePageQueryRefresh, PageEventsOn as EventsOn } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { useCallback, useEffect, useRef, useState } from "react";
import {
    type EngagementWeekSummary,
    type MobileBriefHealth,
} from "@/lib/wails-shim-app";

export interface TodayStatusData {
    week: EngagementWeekSummary | null;
    brief: MobileBriefHealth | null;
    loading: boolean;
    refresh: () => void;
}

/**
 * The two facts under the Today header: the weekly line ("Isoform spoke up 9
 * times this week, you acted on 6") and scheduled brief health (last run,
 * next run, missed runs). Each half fails on its own, so one missing binding
 * never hides the other. Refreshes when a brief is stored and on focus;
 * focus re-reads brief health only once its cached view is stale.
 */
export function useTodayStatus(enabled: boolean): TodayStatusData {
    const [week, setWeek] = useState<EngagementWeekSummary | null>(() => pageQueryCache.peek('GetEngagementWeekSummary') ?? null);
    const [brief, setBrief] = useState<MobileBriefHealth | null>(() => pageQueryCache.peek('GetMobileBriefHealth') ?? null);
    const [loading, setLoading] = useState(() => pageQueryCache.peek('GetEngagementWeekSummary') === undefined || pageQueryCache.peek('GetMobileBriefHealth') === undefined);
    // Bumped by every refresh and on unmount; an answer whose version is
    // stale when it resolves is dropped, so overlapping refreshes (focus plus
    // a stored brief) cannot leave the older answer on screen.
    const version = useRef(0);

    const refresh = useCallback(() => {
        const current = ++version.current;
        Promise.all([
            GetEngagementWeekSummary().catch(() => null),
            GetMobileBriefHealth().catch(() => null),
        ]).then(([nextWeek, nextBrief]) => {
            if (current !== version.current) return;
            setWeek(nextWeek ?? null);
            setBrief(nextBrief ?? null);
            setLoading(false);
        });
    }, []);

    usePageQueryRefresh(['GetEngagementWeekSummary', 'GetMobileBriefHealth'], refresh, enabled);

    useEffect(() => {
        if (!enabled) return;
        refresh();
        const unsubscribe = EventsOn("mobile:updated", refresh);
        return () => {
            version.current += 1;
            unsubscribe();
        };
    }, [enabled, refresh]);

    return { week, brief, loading, refresh: () => { pageQueryCache.invalidate(['GetEngagementWeekSummary', 'GetMobileBriefHealth']); return refresh(); } };
}
