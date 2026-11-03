import { useCallback, useEffect, useRef, useState } from "react";
import { DismissTodayBeforeYouTalk, SetEngagementKindEnabled, type TodayBeforeYouTalk } from "@/lib/wails-shim-app";
import { GetTodayBeforeYouTalk, usePagePrivacyWithdrawal, usePageQueryRefresh } from "@/lib/page-queries";
import { pageQueryCache } from "@/lib/query-cache";
import { activeInterval } from "@/lib/app-activity";

/** Cached meeting preview only. Every cache invalidation clears the local
 * copy and supersedes pending reads, including while Today is retained hidden. */
export function useTodayBeforeYouTalk(enabled: boolean) {
    const [card, setCard] = useState<TodayBeforeYouTalk | null>(null);
    const version = useRef(0);
    const refresh = useCallback(() => {
        const current = ++version.current;
        // Invalidation has already removed the cached value before notifying
        // subscribers. Clear synchronously, before awaiting the gated reader.
        setCard(enabled ? pageQueryCache.peek<TodayBeforeYouTalk>("GetTodayBeforeYouTalk") ?? null : null);
        if (!enabled) return;
        void GetTodayBeforeYouTalk().then((next) => {
            if (current === version.current) setCard(next);
        }, () => {
            if (current === version.current) setCard(null);
        });
    }, [enabled]);
    // Hidden pages still discard private local state, without starting reads.
    usePageQueryRefresh(["GetTodayBeforeYouTalk"], refresh, true);
    useEffect(() => {
        refresh();
        if (!enabled) return;
        // The card follows meeting times: re-read every 30 s while Today is
        // looked at, and at once when it is shown again. Stopped while the
        // window is hidden or the Mac locked.
        const invalidate = () => pageQueryCache.invalidate(["GetTodayBeforeYouTalk"]);
        const stop = activeInterval(invalidate, 30_000);
        return () => {
            version.current++;
            stop();
        };
    }, [enabled, refresh]);
    // Forget, app protection, Closed Eyes and identity withdrawal retire the
    // rendered card and its in-flight reads in the event's own turn; an
    // ordinary quota poll is not a withdrawal and keeps the card.
    usePagePrivacyWithdrawal(() => {
        version.current++;
        setCard(null);
    });
    const dismiss = useCallback(async (meetingID: string) => {
        await DismissTodayBeforeYouTalk(meetingID);
        pageQueryCache.invalidate(["GetTodayBeforeYouTalk"]);
        refresh();
    }, [refresh]);
    const mute = useCallback(async () => {
        await SetEngagementKindEnabled("meeting_prep", false);
        pageQueryCache.invalidate(["GetTodayBeforeYouTalk"]);
        refresh();
    }, [refresh]);
    return { card, dismiss, mute };
}
