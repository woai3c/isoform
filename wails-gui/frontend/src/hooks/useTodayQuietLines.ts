import { GetTodayQuietLines, usePageQueryRefresh } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { useCallback, useEffect, useState } from "react";
import { type QuietLine } from "@/lib/wails-shim-app";
import { useBudgetedRefresh } from "@/lib/app-activity";

/** At most how often Today reads its quiet lines again while it is visible. */
export const QUIET_REFRESH_MS = 30 * 60_000;

/** Today's quiet lines (RC 1 A4, `GetTodayQuietLines`): none until read, and
 *  a failed read keeps the last ones. Each brief and card event reads them
 *  again (page-queries); otherwise at most every 30 minutes while Today is
 *  visible, never while the window is hidden or the Mac locked. */
export function useTodayQuietLines(enabled: boolean): QuietLine[] {
    const [lines, setLines] = useState<QuietLine[]>(() => pageQueryCache.peek('GetTodayQuietLines') ?? []);
    const refresh = useCallback(async () => {
        if (!enabled) return;
        try {
            setLines((await GetTodayQuietLines()) ?? []);
        } catch {
            // Quiet lines are a courtesy: a failed read changes nothing.
        }
    }, [enabled]);
    usePageQueryRefresh(['GetTodayQuietLines'], refresh, enabled);

    useEffect(() => {
        void refresh();
    }, [refresh]);
    useBudgetedRefresh(() => { pageQueryCache.invalidate(['GetTodayQuietLines'], false); void refresh(); }, QUIET_REFRESH_MS, enabled);
    return lines;
}
