import { useCallback, useEffect, useRef, useState } from "react";
import { quietRetry } from "@/lib/chat-failure";
import { SendMainChatWithTopic } from "@/lib/wails-shim-app";
import { listMainChatTopicItemsWithOpenAnswerDelivery } from "@/lib/open-answer-delivery";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { TopicChatItem, TopicChatPage } from "@/components/today/topics";

/** Rows per read; the computer caps a page at 200. */
const PAGE_SIZE = 50;
/** How long the panel says Isoform is working before it reads again anyway. */
const REPLY_WAIT_MS = 180_000;

export type TopicChatStatus = "idle" | "loading" | "ready" | "failed" | "withdrawn";

/** Words sent from the panel, until the main chat holds them. */
export interface TopicChatPending {
    text: string;
    state: "sending" | "failed";
}

/** "sent", "failed", or "stale" when the panel moved to another topic before
 *  the send came back (its words belong to the topic they were sent in). */
export type TopicChatSendResult = "sent" | "failed" | "stale";

interface AgentEventLike {
    type?: string;
    /** The local id of the chat session the run belongs to. */
    tag?: string;
}

const ENDS_A_RUN = new Set(["done", "error", "close"]);

/**
 * The main chat filtered to one topic (RC 1 A3): its messages and the
 * Activity events behind it, oldest first. Every read is live and starts from
 * the latest page again, so a pause or a withdrawn source takes older rows
 * with it; reads happen when the main chat's reply ends, when observing
 * pauses or resumes, and when the window comes back. send() posts the words
 * to the main chat with the topic attached; it never creates another chat.
 */
export function useTopicChat(topicId: string | null) {
    const [items, setItems] = useState<TopicChatItem[]>([]);
    const [status, setStatus] = useState<TopicChatStatus>("idle");
    const [hasMore, setHasMore] = useState(false);
    const [cursor, setCursor] = useState("");
    const [loadingMore, setLoadingMore] = useState(false);
    const [pending, setPending] = useState<TopicChatPending | null>(null);
    const [waiting, setWaiting] = useState(false);
    // Follow queued responses immediately; a render may still be one batch behind.
    const displayed = useRef({ items, status, hasMore, cursor });
    const request = useRef(0);
    const topicRef = useRef(topicId);
    topicRef.current = topicId;
    // The main chat session whose run the panel waits for, and the runs that
    // ended while a send was still on its way.
    const expectedTag = useRef<string | null>(null);
    const sending = useRef(false);
    const endedWhileSending = useRef(new Set<string>());
    const checkingWithdrawal = useRef(false);

    const load = useCallback(async () => {
        if (!topicId) return;
        const id = ++request.current;
        if (displayed.current.status !== "ready" && displayed.current.status !== "loading") {
            displayed.current = { ...displayed.current, status: "loading" };
            setStatus("loading");
        }
        try {
            // A withdrawn or gone topic is an answer, not a hiccup: no quiet retry.
            const page = await quietRetry(
                () => listMainChatTopicItemsWithOpenAnswerDelivery(topicId, "", PAGE_SIZE),
                (error) => !/TODAY_CHAT_(CONTEXT|ITEM)_UNAVAILABLE/.test(String(error)),
            ) as unknown as TopicChatPage;
            if (id !== request.current) return;
            checkingWithdrawal.current = false;
            // A background read of the same rows leaves the detail page and
            // the reader's scroll position untouched. Removed/changed rows still replace it.
            const current = displayed.current;
            const incoming = page.items ?? [];
            const next = JSON.stringify(current.items) === JSON.stringify(incoming) ? current.items : incoming;
            // Reads may finish in one React batch; the next comparison must see
            // this queued response even before the component renders it.
            displayed.current = { items: next, hasMore: page.hasMore, cursor: page.nextCursor, status: "ready" };
            if (next !== current.items) setItems(next);
            if (current.hasMore !== page.hasMore) setHasMore(page.hasMore);
            if (current.cursor !== page.nextCursor) setCursor(page.nextCursor);
            if (current.status !== "ready") setStatus("ready");
        } catch (error) {
            if (id !== request.current) return;
            const withdrawn = checkingWithdrawal.current && (String(error).includes("TODAY_CHAT_CONTEXT_UNAVAILABLE") || String(error).includes("TODAY_CHAT_ITEM_UNAVAILABLE"));
            checkingWithdrawal.current = false;
            if (withdrawn) {
                displayed.current = { items: [], status: "withdrawn", hasMore: false, cursor: "" };
                setItems([]);
                setHasMore(false);
                setCursor("");
                setPending(null);
                setWaiting(false);
                setStatus("withdrawn");
                return;
            }
            // A later failed read keeps what is on screen and says nothing.
            const current = displayed.current.status;
            if (current !== "ready" && current !== "failed" && current !== "withdrawn") {
                displayed.current = { ...displayed.current, status: "failed" };
                setStatus("failed");
            }
        }
    }, [topicId]);

    const loadMore = useCallback(async () => {
        if (!topicId || !hasMore || loadingMore) return;
        setLoadingMore(true);
        const id = request.current;
        try {
            const page = await listMainChatTopicItemsWithOpenAnswerDelivery(topicId, cursor, PAGE_SIZE) as unknown as TopicChatPage;
            // A read that started since replaces everything; drop this page.
            if (id !== request.current) return;
            const next = [...(page.items ?? []), ...displayed.current.items];
            displayed.current = { ...displayed.current, items: next, hasMore: page.hasMore, cursor: page.nextCursor };
            setItems(next);
            setHasMore(page.hasMore);
            setCursor(page.nextCursor);
        } catch {
            // The button stays; the next press reads again.
        } finally {
            setLoadingMore(false);
        }
    }, [topicId, hasMore, cursor, loadingMore]);

    // A new topic starts from its latest page.
    useEffect(() => {
        request.current++;
        checkingWithdrawal.current = false;
        displayed.current = { items: [], status: topicId ? "loading" : "idle", hasMore: false, cursor: "" };
        setItems([]);
        setHasMore(false);
        setCursor("");
        setPending(null);
        setWaiting(false);
        expectedTag.current = null;
        setStatus(topicId ? "loading" : "idle");
        void load();
    }, [topicId, load]);

    useEffect(() => {
        if (!topicId) return;
        const reload = () => void load();
        const offAgent = EventsOn("agent:event", (event: AgentEventLike) => {
            if (!event?.type || !ENDS_A_RUN.has(event.type) || !event.tag) return;
            if (sending.current) endedWhileSending.current.add(event.tag);
            // Only the main chat's own run ends the wait; scheduled runs
            // stream on the same channel under their own sessions.
            if (event.tag === expectedTag.current) {
                expectedTag.current = null;
                setWaiting(false);
                reload();
            }
        });
        const offPause = EventsOn("closed-eyes:changed", reload);
        const offUpdated = EventsOn("topics:updated", reload);
        const offWithdrawn = EventsOn("topics:withdrawn", () => {
            checkingWithdrawal.current = true;
            reload();
        });
        window.addEventListener("focus", reload);
        return () => {
            offAgent();
            offPause();
            offUpdated();
            offWithdrawn();
            window.removeEventListener("focus", reload);
        };
    }, [topicId, load]);

    useEffect(() => {
        if (!waiting) return;
        const timer = window.setTimeout(() => {
            expectedTag.current = null;
            setWaiting(false);
            void load();
        }, REPLY_WAIT_MS);
        return () => window.clearTimeout(timer);
    }, [waiting, load]);

    const send = useCallback(async (text: string): Promise<TopicChatSendResult> => {
        const words = text.trim();
        const forTopic = topicId;
        if (!forTopic || !words) return "failed";
        setPending({ text: words, state: "sending" });
        sending.current = true;
        endedWhileSending.current.clear();
        let sessionId = "";
        try {
            const sent = await SendMainChatWithTopic(words, forTopic) as unknown as { sessionId?: string };
            sessionId = sent?.sessionId ?? "";
        } catch {
            sending.current = false;
            if (topicRef.current !== forTopic) return "stale";
            setPending({ text: words, state: "failed" });
            return "failed";
        }
        sending.current = false;
        if (topicRef.current !== forTopic) return "stale";
        setPending(null);
        if (sessionId && !endedWhileSending.current.has(sessionId)) {
            expectedTag.current = sessionId;
            setWaiting(true);
        }
        await load();
        return "sent";
    }, [topicId, load]);

    return { items, status, hasMore, loadingMore, pending, waiting, reload: load, loadMore, send };
}
