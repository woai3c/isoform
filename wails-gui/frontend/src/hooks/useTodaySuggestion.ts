import { GetTodaySuggestion, usePageQueryRefresh, PageEventsOn as EventsOn } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { useCallback, useEffect, useRef, useState } from "react";
import {
    ResolveTodaySuggestion,
    type TodaySuggestionDecisionKind,
    type TodaySuggestionView,
} from "@/lib/wails-shim-app";

export interface UseTodaySuggestionResult {
    suggestion: TodaySuggestionView | null;
    busy: boolean;
    failed: boolean;
    resolve: (decision: TodaySuggestionDecisionKind, reason?: string) => Promise<boolean>;
    refresh: () => void;
}

/**
 * This week's suggestion for Today. Reads once, again when an initiative
 * changes (the extractor emits initiative:updated), and after each decision.
 */
export function useTodaySuggestion(enabled: boolean): UseTodaySuggestionResult {
    const [suggestion, setSuggestion] = useState<TodaySuggestionView | null>(() => { const view = pageQueryCache.peek<TodaySuggestionView>('GetTodaySuggestion'); return view?.available ? view : null; });
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    const version = useRef(0);

    const refresh = useCallback(() => {
        const current = ++version.current;
        GetTodaySuggestion()
            .then((view) => {
                if (current !== version.current) return;
                setSuggestion(view.available ? view : null);
            })
            .catch(() => {
                if (current === version.current) setSuggestion(null);
            });
    }, []);

    usePageQueryRefresh(['GetTodaySuggestion'], refresh, enabled);

    useEffect(() => {
        if (!enabled) return;
        refresh();
        const unsubscribe = EventsOn("initiative:updated", refresh);
        return () => {
            version.current += 1;
            unsubscribe();
        };
    }, [enabled, refresh]);

    const resolve = useCallback(
        async (decision: TodaySuggestionDecisionKind, reason = "") => {
            if (!suggestion || busy) return false;
            setBusy(true);
            setFailed(false);
            try {
                const next = await ResolveTodaySuggestion({ id: suggestion.id, decision, reason });
                pageQueryCache.invalidate(['GetTodaySuggestion']);
                version.current += 1;
                setSuggestion(next.available ? next : null);
                return true;
            } catch {
                setFailed(true);
                return false;
            } finally {
                setBusy(false);
            }
        },
        [suggestion, busy],
    );

    return { suggestion, busy, failed, resolve, refresh: () => { pageQueryCache.invalidate(['GetTodaySuggestion']); return refresh(); } };
}
