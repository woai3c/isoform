import { markLaunchPaint, markLaunchStage } from '@/lib/launch-perf';
import { GetLatestBrief, usePageQueryRefresh, PageEventsOn as EventsOn } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { useBudgetedRefresh } from '@/lib/app-activity';
import { useCallback, useEffect, useRef, useState } from "react";
import { readWithQuietRetry } from "@/lib/quiet-retry";
import {
    GetTodayBriefByID,
    GenerateMobileBriefNow,
    GenerateTodayMissedBrief,
    type TodayBriefView,
} from "@/lib/wails-shim-app";

/** What happened to the last "Make my brief now". */
export type MakeBriefState = "idle" | "making" | "cooldown" | "failed";
export type MakeBriefResult = "started" | "cooldown" | "failed";

export interface UseLatestBriefResult {
    view: TodayBriefView | null;
    loading: boolean;
    /** The brief could not be read at all. */
    loadFailed: boolean;
    make: MakeBriefState;
    /** When "Make my brief now" can run again (RFC 3339), after a cooldown. */
    nextAllowedAt: string;
    makeNow: (requestToken?: string) => Promise<MakeBriefResult>;
    refresh: () => void;
}

// While a brief is being made, Today follows the build's "brief:building"
// event (Go mobileBriefBuildingEvent), never a poll: a build that ends without
// storing a brief is a failure. The give-up is only the safety net for a lost
// event.
const MAKING_GIVE_UP_MS = 4 * 60_000;
/** On focus or show, the brief and its local signals are read again only when
 *  this old; events bring everything else. */
export const BRIEF_RESYNC_MS = 15 * 60_000;

/**
 * Today's brief: the brief the brief service stored for today (the same
 * document the phone reads), or when the next one comes and what Isoform
 * cannot see. Local source events refresh the stored signals between slots.
 */
export function useLatestBrief(enabled: boolean, briefId?: string, requestNonce?: number): UseLatestBriefResult {
    const [view, setView] = useState<TodayBriefView | null>(() => pageQueryCache.peek('GetLatestBrief') ?? null);
    const [loading, setLoading] = useState(() => pageQueryCache.peek('GetLatestBrief') === undefined);
    const [loadFailed, setLoadFailed] = useState(false);
    const [make, setMake] = useState<MakeBriefState>("idle");
    const [nextAllowedAt, setNextAllowedAt] = useState("");
    const version = useRef(0);
    const makingSince = useRef(0);
    // The build said it stopped; the next read decides done or failed.
    const buildEnded = useRef(false);
    // When the last build stopped, for one that ended before its "started"
    // answer arrived.
    const lastBuildEnd = useRef(0);
    // A brief stored after an older one was opened (a Your week notice) is
    // the one to show: Today follows the latest again until the next request.
    const followLatest = useRef(false);
    useEffect(() => { followLatest.current = false; }, [briefId, requestNonce]);

    const refresh = useCallback(() => {
        const current = ++version.current;
        const read = () => briefId && !followLatest.current
            ? GetTodayBriefByID(briefId).then((requested) => requested.brief ? requested : GetLatestBrief())
            : GetLatestBrief();
        readWithQuietRetry(read, () => current === version.current)
            .then((next) => {
                if (current !== version.current) return;
                markLaunchStage("first_data");
                if (next.brief && !next.empty) markLaunchPaint("brief_rendered");
                setView(next);
                setLoadFailed(false);
                setLoading(false);
                if (makingSince.current > 0 && buildEnded.current && !next.building) {
                    // The build ended; a stored brief arrives through its
                    // event, so ending here without one means it failed.
                    makingSince.current = 0;
                    buildEnded.current = false;
                    setMake(next.brief ? "idle" : "failed");
                }
            })
            .catch(() => {
                if (current !== version.current) return;
                setLoadFailed(true);
                setLoading(false);
            });
    }, [briefId, requestNonce]);

    usePageQueryRefresh(['GetLatestBrief'], refresh, enabled);

    useEffect(() => {
        if (!enabled) return;
        refresh();
        const onStored = (storedId?: unknown) => {
            if (briefId && typeof storedId === "string" && storedId !== briefId) followLatest.current = true;
            makingSince.current = 0;
            buildEnded.current = false;
            setMake("idle");
            refresh();
        };
        const onBuilding = (building?: unknown) => {
            if (building === false) {
                lastBuildEnd.current = Date.now();
                if (makingSince.current > 0) buildEnded.current = true;
            }
            refresh();
        };
        const unsubscribers = [
            EventsOn("brief:stored", onStored),
            EventsOn("brief:building", onBuilding),
            EventsOn("mobile:updated", refresh),
            EventsOn("cron:state", refresh),
            EventsOn("handoff:created", refresh),
            EventsOn("handoff:updated", refresh),
            EventsOn("insight:saved", refresh),
            EventsOn("memory:updated", refresh),
            EventsOn("activity:summary-ready", refresh),
            EventsOn("deliverables:updated", refresh),
            // A goal or follow-up added or moved (Activity's Add follow-up,
            // the main chat's goal tools) changes "What needs your attention".
            EventsOn("agency:updated", refresh),
        ];
        return () => {
            version.current += 1;
            unsubscribers.forEach((off) => off());
        };
    }, [enabled, refresh]);
    // Focus and showing the window paint from what Today has; the brief and
    // its local signals are read again only once they are 15 minutes old (a
    // new day after the Mac slept, a missed event). The read keeps the shown
    // brief until its replacement arrives.
    useBudgetedRefresh(() => { pageQueryCache.invalidate(['GetLatestBrief'], false); refresh(); }, BRIEF_RESYNC_MS, enabled);

    // "Make my brief now" comes back when the cooldown ends.
    useEffect(() => {
        if (make !== "cooldown") return;
        const wait = Date.parse(nextAllowedAt) - Date.now();
        const timer = window.setTimeout(() => setMake("idle"), Number.isFinite(wait) ? Math.max(wait, 0) + 500 : 0);
        return () => window.clearTimeout(timer);
    }, [make, nextAllowedAt]);

    useEffect(() => {
        if (make !== "making") return;
        const timer = window.setTimeout(() => {
            if (makingSince.current === 0) return;
            makingSince.current = 0;
            buildEnded.current = false;
            setMake("failed");
        }, MAKING_GIVE_UP_MS);
        return () => window.clearTimeout(timer);
    }, [make]);

    const makeNow = useCallback(async (requestToken?: string): Promise<MakeBriefResult> => {
        try {
            const requestedAt = Date.now();
            const result = await (requestToken ? GenerateTodayMissedBrief(requestToken) : GenerateMobileBriefNow());
            pageQueryCache.invalidate(['GetLatestBrief']);
            if (result.status === "cooldown") {
                setNextAllowedAt(result.nextAllowedAt);
                setMake("cooldown");
                return "cooldown";
            }
            makingSince.current = Date.now();
            buildEnded.current = lastBuildEnd.current >= requestedAt;
            setMake("making");
            refresh();
            return "started";
        } catch {
            setMake("failed");
            return "failed";
        }
    }, [refresh]);

    return { view, loading, loadFailed, make, nextAllowedAt, makeNow, refresh: () => { pageQueryCache.invalidate(['GetLatestBrief']); return refresh(); } };
}
