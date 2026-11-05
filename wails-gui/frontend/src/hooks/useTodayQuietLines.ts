import { GetTodayQuietLines, usePageQueryRefresh } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { useCallback, useEffect, useState } from "react";
import { type QuietLine } from "@/lib/wails-shim-app";

/** Today's quiet lines (RC 1 A4, `GetTodayQuietLines`): none until read, and
 *  a failed read keeps the last ones. Each brief and notice-settings event reads
 *  them again (page-queries), and once on showing or unlocking the window. No timer. */
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
    return lines;
}
