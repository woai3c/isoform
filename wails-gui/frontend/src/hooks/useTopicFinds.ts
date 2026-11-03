import { GetTopicFinds, usePageQueryRefresh, PageEventsOn as EventsOn } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { useCallback, useEffect, useRef, useState } from "react";
import {
    FindNewOnTopics,
    RecordTopicFindFeedback,
    type TopicFindFeedbackAction,
    type TopicFindsView,
} from "@/lib/wails-shim-app";

export interface UseTopicFindsResult {
    view: TopicFindsView | null;
    loading: boolean;
    /** The section could not be read at all. */
    loadFailed: boolean;
    /** The last action failed: starting a search, or saving an answer. */
    failure: "find" | "answer" | null;
    findNew: () => Promise<void>;
    feedback: (findId: string, action: TopicFindFeedbackAction) => Promise<boolean>;
    refresh: () => void;
}

/**
 * "New on your topics": the day's finds, the search state, and the answers
 * (open, more like this, less like this, not interested). Re-reads when a
 * search starts or ends and when the topic list changes.
 */
export function useTopicFinds(enabled: boolean): UseTopicFindsResult {
    const [view, setView] = useState<TopicFindsView | null>(() => pageQueryCache.peek('GetTopicFinds') ?? null);
    const [loading, setLoading] = useState(() => pageQueryCache.peek('GetTopicFinds') === undefined);
    const [loadFailed, setLoadFailed] = useState(false);
    const [failure, setFailure] = useState<"find" | "answer" | null>(null);
    const version = useRef(0);

    const refresh = useCallback(() => {
        const current = ++version.current;
        GetTopicFinds()
            .then((next) => {
                if (current !== version.current) return;
                setView(next);
                setLoadFailed(false);
                setLoading(false);
            })
            .catch(() => {
                if (current !== version.current) return;
                setLoadFailed(true);
                setLoading(false);
            });
    }, []);

    usePageQueryRefresh(['GetTopicFinds'], refresh, enabled);

    useEffect(() => {
        if (!enabled) return;
        refresh();
        const unsubscribers = [
            EventsOn("topics:finds", refresh),
            EventsOn("topics:updated", refresh),
            // Whether the search can run follows the assistant and its
            // models, which may still be loading when Today first reads.
            EventsOn("model-accounts:updated", refresh),
            EventsOn("ai:config-changed", refresh),
        ];
        return () => {
            version.current += 1;
            unsubscribers.forEach((off) => off());
        };
    }, [enabled, refresh]);

    const findNew = useCallback(async () => {
        setFailure(null);
        try {
            const next = await FindNewOnTopics();
            pageQueryCache.invalidate(['GetTopicFinds']);
            version.current += 1;
            setView(next);
        } catch {
            setFailure("find");
        }
    }, []);

    const feedback = useCallback(async (findId: string, action: TopicFindFeedbackAction) => {
        setFailure(null);
        try {
            const next = await RecordTopicFindFeedback({ findId, action });
            pageQueryCache.invalidate(['GetTopicFinds']);
            version.current += 1;
            setView(next);
            return true;
        } catch {
            setFailure("answer");
            return false;
        }
    }, []);

    return { view, loading, loadFailed, failure, findNew, feedback, refresh: () => { pageQueryCache.invalidate(['GetTopicFinds']); return refresh(); } };
}
