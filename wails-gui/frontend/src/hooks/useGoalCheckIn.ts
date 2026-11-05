import { GetGoalCheckIn, usePageQueryRefresh } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { useCallback, useEffect, useRef, useState } from "react";
import { type GoalCheckInView } from "@/lib/wails-shim-app";

/** Today's one check-in for a goal long past due (Go: GetGoalCheckIn), read
 *  again when a goal moves, when observation is paused or resumed (Go asks
 *  nothing while paused), and when the window comes back. */
export function useGoalCheckIn(enabled: boolean): { checkIn: GoalCheckInView | null; refresh: () => void } {
    const [checkIn, setCheckIn] = useState<GoalCheckInView | null>(() => pageQueryCache.peek('GetGoalCheckIn') ?? null);
    const version = useRef(0);
    const refresh = useCallback(() => {
        const mine = ++version.current;
        GetGoalCheckIn()
            .then((next) => {
                if (mine === version.current) setCheckIn(next ?? null);
            })
            .catch(() => {
                if (mine === version.current) setCheckIn(null);
            });
    }, []);
    usePageQueryRefresh(['GetGoalCheckIn'], refresh, enabled);

    useEffect(() => {
        if (!enabled) return;
        refresh();
        // agency:updated and closed-eyes:changed re-read through the page-query
        // bus; showing the window invalidates Today's reads once (no focus read).
        return () => {
            version.current += 1;
        };
    }, [enabled, refresh]);
    return { checkIn: enabled ? checkIn : null, refresh: () => { pageQueryCache.invalidate(['GetGoalCheckIn']); return refresh(); } };
}
