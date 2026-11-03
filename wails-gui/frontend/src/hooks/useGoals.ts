import { useCallback, useEffect, useRef, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { ListGoals, usePagePrivacyWithdrawal, usePageQueryRefresh } from "@/lib/page-queries";
import { pageQueryCache } from "@/lib/query-cache";
import { type GoalsPageView } from "@/lib/wails-shim-app";
import { markPageContent } from "@/lib/page-switch-timing";

/** The Goals page's lists (Go: ListGoals), read again when a goal moves on
 *  either device and when the window comes back. A failed read keeps the
 *  lists already on screen. A warm cache paints at once; the revalidation
 *  then replaces it in the background. */
export function useGoals(enabled: boolean): { page: GoalsPageView | null; loading: boolean; failed: boolean; refresh: () => void } {
    const [page, setPage] = useState<GoalsPageView | null>(() => pageQueryCache.peek<GoalsPageView>('ListGoals') ?? null);
    const [loading, setLoading] = useState(page === null);
    const [failed, setFailed] = useState(false);
    const version = useRef(0);
    const refresh = useCallback(() => {
        const mine = ++version.current;
        ListGoals()
            .then((next) => {
                if (mine !== version.current) return;
                setPage(next);
                setFailed(false);
                markPageContent('goals');
            })
            .catch(() => {
                if (mine === version.current) setFailed(true);
            })
            .finally(() => {
                if (mine === version.current) setLoading(false);
            });
    }, []);
    usePageQueryRefresh(['ListGoals'], refresh, enabled);
    // A privacy transition withdraws the pre-transition lists synchronously,
    // even while the retained page is hidden; the sweep's refresh (or the next
    // activation) re-reads what is still permitted.
    usePagePrivacyWithdrawal(useCallback(() => {
        version.current += 1;
        setPage(null);
        setLoading(true);
        setFailed(false);
    }, []));
    // A seeded first paint already has content; only a cold load waits on the
    // network read's mark.
    const seeded = useRef(page !== null);
    useEffect(() => {
        if (seeded.current) markPageContent('goals');
    }, []);
    useEffect(() => {
        if (!enabled) return;
        refresh();
        const off = EventsOn("agency:updated", refresh);
        window.addEventListener("focus", refresh);
        return () => {
            version.current += 1;
            off();
            window.removeEventListener("focus", refresh);
        };
    }, [enabled, refresh]);
    return { page, loading, failed, refresh };
}
