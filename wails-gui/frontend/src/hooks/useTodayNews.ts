import { GetTodayNews, usePageQueryRefresh, PageEventsOn as EventsOn } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { useCallback, useEffect, useRef, useState } from "react";
import {
    GenerateTodayNews,
    GetTodayNewsHistory,
} from "@/lib/wails-shim-app";
import type { TodayNewsData } from "@/components/today/types";

const EMPTY: TodayNewsData = {
    date: "",
    status: "none",
    items: [],
    updatedAt: 0,
};

// Go marshals a nil slice to JSON `null`, so `items` can arrive null for any
// non-"ready" status — always coerce to an array before it reaches the UI.
function normalize(d: TodayNewsData | null): TodayNewsData {
    if (!d) return EMPTY;
    return { ...d, items: Array.isArray(d.items) ? d.items : [] };
}

export interface UseTodayNewsResult {
    data: TodayNewsData;
    loading: boolean;
    regenerate: () => void;
}

/**
 * Loads today's news, which is produced by a hidden "Daily News" automation
 * (cron job). GetTodayNews reads that job's latest run; if the job hasn't run
 * yet we call GenerateTodayNews (ensure job + run now). A `cron:run` event
 * fires when any automation finishes, so we refetch then.
 */
export function useTodayNews(enabled: boolean): UseTodayNewsResult {
    const active = useRef(enabled);
    active.current = enabled;
    const [data, setData] = useState<TodayNewsData>(() => normalize(pageQueryCache.peek('GetTodayNews') ?? null));
    const [loading, setLoading] = useState(() => pageQueryCache.peek('GetTodayNews') === undefined);

    const fetchNews = useCallback(async (): Promise<TodayNewsData> => {
        try {
            const next = normalize(await GetTodayNews());
            setData(next);
            return next;
        } catch {
            const errored: TodayNewsData = { ...EMPTY, status: "error" };
            setData(errored);
            return errored;
        }
    }, []);

    const regenerate = useCallback(() => {
        pageQueryCache.set('GetTodayNews', [], { ...(pageQueryCache.peek<TodayNewsData>('GetTodayNews') ?? EMPTY), status: 'generating' });
        setData((prev) => ({ ...prev, status: "generating" }));
        void GenerateTodayNews().catch(() => {
            // Restore the authoritative state when dispatch fails (for example,
            // if another surface already started this job).
            pageQueryCache.invalidate(['GetTodayNews']);
            void fetchNews();
        });
    }, [fetchNews]);

    usePageQueryRefresh(['GetTodayNews'], fetchNews, enabled);

    useEffect(() => {
        if (!enabled) return;
        let cancelled = false;
        setLoading(pageQueryCache.peek('GetTodayNews') === undefined);
        void fetchNews().then(async (d) => {
            if (cancelled || !active.current) return;
            // A stale "none" can paint immediately, but only the completed
            // read may decide whether automatic generation is still needed.
            if (d.status === "none") {
                const entry = pageQueryCache.state('GetTodayNews');
                try {
                    if (entry?.pending) d = normalize(await entry.pending as TodayNewsData);
                    else if (entry?.error) d = { ...EMPTY, status: "error" };
                    else if (entry?.ready) d = normalize(entry.value as TodayNewsData);
                } catch {
                    d = { ...EMPTY, status: "error" };
                }
            }
            if (cancelled || !active.current) return;
            setData(d);
            setLoading(false);
            // No automation / no successful run yet — provision + run it once.
            if (d.status === "none") {
                pageQueryCache.set('GetTodayNews', [], { ...d, status: 'generating' });
                setData((prev) => ({ ...prev, status: "generating" }));
                void GenerateTodayNews().catch(() => {});
            }
        });

        // The Daily News automation is hidden, so we don't hold its job id here;
        // refetch whenever any cron run completes (infrequent, and GetTodayNews
        // is a cheap read). A missed time is not a run, and a Mac waking up
        // records many at once, so those do not refetch.
        const unsubs = [
            EventsOn("cron:run", (run?: { status?: string }) => {
                if (run?.status === "missed") return;
                void fetchNews();
            }),
            // Whether news can be generated depends on the model gateway being
            // up; a first read at launch can land before it is, so re-read when
            // the accounts status changes.
            EventsOn("model-accounts:updated", () => {
                void fetchNews();
            }),
            // A changed assistant can change whether and how news searches.
            EventsOn("ai:config-changed", () => {
                void fetchNews();
            }),
        ];

        return () => {
            cancelled = true;
            for (const unsub of unsubs) unsub();
        };
    }, [enabled, fetchNews]);

    return { data, loading, regenerate };
}

const HISTORY_PAGE_SIZE = 3;

export interface UseTodayNewsHistoryResult {
    days: TodayNewsData[];
    loadingMore: boolean;
    hasMore: boolean;
    loadMore: () => void;
}

/**
 * Loads previous days' news briefs from the Daily News automation's run
 * history, page by page, for the Today page's scroll-down review.
 *
 * `anchorDate` is the local date of the brief currently shown at the top
 * (history starts strictly before it); pass null while that brief is still
 * loading — fetching is gated until the anchor is known, and a changed anchor
 * (e.g. a refresh produced today's first run) resets the list so days are
 * never duplicated or skipped.
 */
export function useTodayNewsHistory(
    anchorDate: string | null,
): UseTodayNewsHistoryResult {
    const [days, setDays] = useState<TodayNewsData[]>([]);
    const [loadingMore, setLoadingMore] = useState(false);
    const [hasMore, setHasMore] = useState(true);
    const daysRef = useRef<TodayNewsData[]>([]);
    const hasMoreRef = useRef(true);
    const inFlightRef = useRef(false);

    useEffect(() => {
        // New anchor — start over from the day before it.
        daysRef.current = [];
        hasMoreRef.current = anchorDate != null;
        setDays([]);
        setHasMore(anchorDate != null);
    }, [anchorDate]);

    const loadMore = useCallback(() => {
        if (!anchorDate || inFlightRef.current || !hasMoreRef.current) return;
        inFlightRef.current = true;
        setLoadingMore(true);
        const loaded = daysRef.current;
        const before = loaded.length
            ? loaded[loaded.length - 1].date
            : anchorDate;
        GetTodayNewsHistory(before, HISTORY_PAGE_SIZE)
            .then((res) => {
                const batch = (res ?? [])
                    .map(normalize)
                    .filter((d) => d.items.length > 0);
                const next = [...daysRef.current, ...batch];
                daysRef.current = next;
                setDays(next);
                const more = (res ?? []).length === HISTORY_PAGE_SIZE;
                hasMoreRef.current = more;
                setHasMore(more);
            })
            .catch(() => {
                hasMoreRef.current = false;
                setHasMore(false);
            })
            .finally(() => {
                inFlightRef.current = false;
                setLoadingMore(false);
            });
    }, [anchorDate]);

    return { days, loadingMore, hasMore, loadMore };
}
