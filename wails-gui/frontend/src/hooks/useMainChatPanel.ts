import { markLaunchPaint } from '@/lib/launch-perf';
import { GetMainChat, LoadStreamItemsPage, usePageQueryRefresh, PageEventsOn as EventsOn } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { useCallback, useEffect, useRef, useState } from "react";
import { AskMainChat, AskMainChatFromTodayChip, AskMainChatWithContext, CancelStream, SendMainChat, SendMainChatFromTodayChip, SendMainChatWithAttachments, SendMainChatWithContext } from "@/lib/wails-shim-app";
import type { TodayChatContextRef } from "@/lib/today-chat-navigation";
import { fromStreamItemData, getMainChatProactive } from "@/lib/stream-helpers";
import type { FileAttachment, ImageAttachment, StreamItem } from "@/types";
import { mainChatStreamRows } from "@/lib/main-chat-view";

/** Rows per read; the computer caps a page at 200. */
const PAGE_SIZE = 50;
/** How long the panel says Isoform is working before it reads again anyway. */
const REPLY_WAIT_MS = 180_000;
/** The latest page. */
const LATEST = -1;

export type MainChatPanelStatus = "loading" | "ready" | "failed";

/** Words sent from the panel, until the main chat holds them. */
export interface MainChatPanelPending {
    text: string;
    state: "sending" | "failed";
    context?: TodayChatContextRef | null;
    ask?: boolean;
    firstChip?: boolean;
    answerChip?: boolean;
}

interface AgentEventLike {
    type?: string;
    /** The local id of the chat session the run belongs to. */
    tag?: string;
    run_id?: string;
    item_id?: string;
    item_seq?: number;
    content?: string;
}

interface PageWire {
    items?: Parameters<typeof fromStreamItemData>[0][] | null;
    hasMore?: boolean;
    nextCursor?: number;
}

const ENDS_A_RUN = new Set(["done", "error", "close"]);

/** The panel and the page feed the same rows to the shared stream. */
export function panelRows(items: readonly StreamItem[]): StreamItem[] {
    return mainChatStreamRows(items);
}

/** The cards still waiting on the user, in the chat's order. */
export function waitingCards(items: readonly StreamItem[]): StreamItem[] {
    return items.filter((item) => item.rowClass === "waiting" && getMainChatProactive(item) !== null);
}

/**
 * The whole main chat for Today's panel while no topic is open (Claude
 * Design, 2026-10-22): its newest messages, unfiltered, read through the
 * main chat's own page read, and a plain send with no topic attached. Reads
 * happen when the main chat's run ends, when a row is appended, when
 * observing pauses or resumes, and when the window comes back.
 */
export function useMainChatPanel(enabled: boolean) {
    const cachedId = pageQueryCache.peek<{ session?: { localId?: string } }>('GetMainChat')?.session?.localId ?? '';
    const cachedPage = pageQueryCache.peek<PageWire>('LoadStreamItemsPage', [cachedId, LATEST, PAGE_SIZE]);
    const [sessionId, setSessionId] = useState(cachedId);
    const [items, setItems] = useState<StreamItem[]>(() => (cachedPage?.items ?? []).map(fromStreamItemData));
    const [status, setStatus] = useState<MainChatPanelStatus>(cachedPage ? 'ready' : 'loading');
    const [hasMore, setHasMore] = useState(cachedPage?.hasMore === true);
    const [cursor, setCursor] = useState(cachedPage?.nextCursor ?? LATEST);
    const [loadingMore, setLoadingMore] = useState(false);
    const [pending, setPending] = useState<MainChatPanelPending | null>(null);
    const [waiting, setWaiting] = useState(false);
    const [liveRows, setLiveRows] = useState<StreamItem[]>([]);
    const [runId, setRunId] = useState("");
    const [stopped, setStopped] = useState(false);
    const [lastUserItemId, setLastUserItemId] = useState("");
    const request = useRef(0);
    const sessionRef = useRef(cachedId);
    const expectedTag = useRef<string | null>(null);
    const sending = useRef(false);
    const endedWhileSending = useRef(new Set<string>());
    const currentRun = useRef("");
    const liveSequence = useRef(new Map<string, number>());
    const sendGeneration = useRef(0);
    const lastSent = useRef<{ text: string; context?: TodayChatContextRef | null; ask?: boolean; firstChip?: boolean; answerChip?: boolean } | null>(null);

    const mainChatId = useCallback(async (): Promise<string> => {
        if (sessionRef.current) return sessionRef.current;
        const info = await GetMainChat() as unknown as { session?: { localId?: string } };
        const id = info?.session?.localId ?? "";
        if (!id) throw new Error("MAIN_CHAT_UNAVAILABLE");
        sessionRef.current = id;
        setSessionId(id);
        return id;
    }, []);

    const load = useCallback(async () => {
        if (!enabled) return;
        const id = ++request.current;
        setStatus((current) => (current === "ready" ? current : "loading"));
        try {
            const main = await mainChatId();
            const page = ((await LoadStreamItemsPage(main, LATEST, PAGE_SIZE)) ?? {}) as PageWire;
            if (id !== request.current) return;
            setItems((page.items ?? []).map(fromStreamItemData));
            setHasMore(page.hasMore === true);
            setCursor(typeof page.nextCursor === "number" ? page.nextCursor : LATEST);
            setStatus("ready");
            markLaunchPaint("chat_rendered");
        } catch {
            // A later failed read keeps what is on screen and says nothing.
            if (id === request.current) setStatus((current) => (current === "ready" ? current : "failed"));
        }
    }, [enabled, mainChatId]);

    const forceReload = useCallback(() => {
        pageQueryCache.invalidate(['LoadStreamItemsPage']);
        return load();
    }, [load]);

    const loadMore = useCallback(async () => {
        if (!enabled || !hasMore || loadingMore || !sessionRef.current) return;
        setLoadingMore(true);
        const id = request.current;
        try {
            const page = ((await LoadStreamItemsPage(sessionRef.current, cursor, PAGE_SIZE)) ?? {}) as PageWire;
            // A read that started since replaces everything; drop this page.
            if (id !== request.current) return;
            setItems((current) => [...(page.items ?? []).map(fromStreamItemData), ...current]);
            setHasMore(page.hasMore === true);
            setCursor(typeof page.nextCursor === "number" ? page.nextCursor : LATEST);
        } catch {
            // The button stays; the next press reads again.
        } finally {
            setLoadingMore(false);
        }
    }, [enabled, hasMore, cursor, loadingMore]);

    usePageQueryRefresh(['LoadStreamItemsPage'], load, enabled, (args) => args[0] === sessionRef.current && args[1] === LATEST && args[2] === PAGE_SIZE);

    useEffect(() => {
        request.current++;
        const page = pageQueryCache.peek<PageWire>('LoadStreamItemsPage', [sessionRef.current, LATEST, PAGE_SIZE]);
        setItems((page?.items ?? []).map(fromStreamItemData));
        setHasMore(page?.hasMore === true);
        setCursor(page?.nextCursor ?? LATEST);
        setPending(null);
        setWaiting(false);
        setLiveRows([]);
        setRunId("");
        currentRun.current = "";
        expectedTag.current = null;
        setStatus(page ? 'ready' : 'loading');
        if (enabled) void load();
    }, [enabled, load]);

    useEffect(() => {
        if (!enabled) return;
        const reload = () => void load();
        const offAgent = EventsOn("agent:event", (event: AgentEventLike) => {
            if (!event?.type || !event.tag || event.tag !== sessionRef.current) return;
            if (event.type === "run_created" && event.run_id && (sending.current || expectedTag.current === event.tag)) {
                currentRun.current = event.run_id;
                setRunId(event.run_id);
                return;
            }
            if (event.type === "assistant" && event.content && event.item_id && currentRun.current && event.run_id === currentRun.current) {
                const chunk = event.content;
                const last = liveSequence.current.get(event.item_id) ?? 0;
                const seq = event.item_seq ?? 0;
                if (seq && seq !== last + 1) return;
                liveSequence.current.set(event.item_id, seq || last + 1);
                setLiveRows((rows) => {
                    const existing = rows.find((row) => row.id === event.item_id);
                    return existing ? rows.map((row) => row.id === event.item_id ? { ...row, content: (row.content ?? "") + chunk } : row)
                        : [...rows, { id: event.item_id!, kind: "assistant", content: chunk }];
                });
                return;
            }
            if (!ENDS_A_RUN.has(event.type)) return;
            if (sending.current) endedWhileSending.current.add(event.tag);
            if (event.run_id && currentRun.current && event.run_id !== currentRun.current) return;
            // Any run of the main chat ends the wait and shows its rows.
            expectedTag.current = null;
            setWaiting(false);
            currentRun.current = "";
            setRunId("");
            const generation = sendGeneration.current;
            void load().then(() => { if (generation === sendGeneration.current) setLiveRows([]); });
        });
        const offAppended = EventsOn("session:items-appended", (data: { sessionId?: unknown } | null) => {
            if (data?.sessionId === sessionRef.current) reload();
        });
        const offPause = EventsOn("closed-eyes:changed", () => { currentRun.current = ""; setRunId(""); setLiveRows([]); reload(); });
        const onFocus = () => { void forceReload(); };
        window.addEventListener("focus", onFocus);
        return () => {
            offAgent();
            offAppended();
            offPause();
            window.removeEventListener("focus", onFocus);
        };
    }, [enabled, load, forceReload]);

    useEffect(() => {
        if (!waiting) return;
        const timer = window.setTimeout(() => {
            expectedTag.current = null;
            setWaiting(false);
            void forceReload();
        }, REPLY_WAIT_MS);
        return () => window.clearTimeout(timer);
    }, [waiting, forceReload]);

    /** Sends the words to the main chat; with a Today item (an answer
     *  prepared from the brief) the item goes with them and is checked
     *  again at send, as the main chat's own box does. */
    const send = useCallback(async (text: string, ref?: TodayChatContextRef | null, ask = false, firstChip = false, answerChip = false, attachments?: { images: ImageAttachment[]; files: FileAttachment[] }): Promise<"sent" | "failed"> => {
        const words = text.trim();
        if (!words) return "failed";
        if (sending.current || expectedTag.current) {
            setPending({ text: words, state: "failed", context: ref, ask, firstChip, answerChip });
            return "failed";
        }
        sendGeneration.current++;
        setLastUserItemId("");
        setStopped(false);
        setLiveRows([]);
        liveSequence.current.clear();
        currentRun.current = "";
        setRunId("");
        setPending({ text: words, state: "sending", context: ref, ask, firstChip, answerChip });
        sending.current = true;
        endedWhileSending.current.clear();
        let tag = "";
        try {
            await mainChatId();
            const sent = (ask ? (ref ? await AskMainChatWithContext(words, ref) : firstChip ? await AskMainChatFromTodayChip(words) : await AskMainChat(words)) : (ref ? await SendMainChatWithContext(words, ref) : answerChip ? await SendMainChatFromTodayChip(words)
                : attachments && (attachments.images.length > 0 || attachments.files.length > 0)
                    ? await SendMainChatWithAttachments(words,
                        attachments.images.map((image) => ({ media_type: image.mediaType, data_base64: image.dataBase64 })),
                        attachments.files.map((file) => ({ file_name: file.fileName, media_type: file.mediaType, data_base64: file.dataBase64 ?? "", file_size: file.fileSize })))
                    : await SendMainChat(words))) as unknown as { sessionId?: string; userItemId?: string };
            tag = sent?.sessionId ?? "";
            setLastUserItemId(sent?.userItemId ?? "");
        } catch {
            sending.current = false;
            setPending({ text: words, state: "failed", context: ref, ask, firstChip, answerChip });
            return "failed";
        }
        sending.current = false;
        setPending((current) => current?.state === "failed" ? current : null);
        lastSent.current = { text: words, context: ref, ask, firstChip, answerChip };
        if (tag && !endedWhileSending.current.has(tag)) {
            expectedTag.current = tag;
            setWaiting(true);
        }
        pageQueryCache.invalidate(['LoadStreamItemsPage']);
        await load();
        return "sent";
    }, [load, mainChatId]);

    const stop = useCallback(async () => {
        const id = currentRun.current;
        if (!id || !expectedTag.current) return;
        await CancelStream(id);
        setStopped(true);
    }, []);
    const startOver = useCallback(async () => {
        const last = lastSent.current;
        if (last) await send(last.text, last.context, last.ask, last.firstChip, last.answerChip);
    }, [send]);

    const shownItems = items.map((item) => liveRows.find((live) => live.id === item.id) ?? item);
    const known = new Set(items.map((item) => item.id));
    shownItems.push(...liveRows.filter((row) => !known.has(row.id)));

    return { sessionId, items: shownItems, status, hasMore, cursor, loadingMore, pending, waiting, runId, stopped, lastUserItemId, stop, startOver, reload: forceReload, loadMore, send };
}
