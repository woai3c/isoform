import { measureToday, recordTodayAction } from "@/lib/today-telemetry";
import { useContext, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, Loader2, X } from "lucide-react";
import { useTopicRefreshIndicator } from "@/hooks/useTopicRefreshIndicator";
import { useReplyNavigation } from "@/hooks/useReplyNavigation";
import { Button } from "@/components/ui/button";
import { SheetClose, SheetTitle } from "@/components/ui/sheet";
import type { TodayChatContextRef, TodayChatPreview } from "@/lib/today-chat-navigation";
import { TodayContextPreview } from "@/components/today-context-status";
import { cn } from "@/lib/utils";
import { useMainChatPanel } from "@/hooks/useMainChatPanel";
import { useTasksNeedsYouCount } from "@/hooks/useTasksNeedsYou";
import { useMainChatAnswerChips } from "@/hooks/useMainChatView";
import { answerChipIds, localDayKey } from "@/lib/main-chat-view";
import { type MainChatReportTarget } from "@/lib/stream-helpers";
import { MainChatViewContext } from "@/components/main-chat/main-chat-view-context";
import { GroupedStreamList } from "@/components/grouped-stream-list";
import { MainChatItemPanel } from "@/components/main-chat-item-panel";
import { useMainChatItemPanel } from "@/hooks/useMainChatItemPanel";
import { TopicComposer } from "./TopicComposer";
import { TopicRow } from "./TopicPanel";
import type { TodayActionHandlers } from "./types";
import type { TopicChatItem } from "./topics";
import type { FileAttachment, ImageAttachment, StreamItem } from "@/types";
import { ComposerVoiceContext } from "@/lib/composer-voice-context";
import { ACCEPTED_FILE_EXTENSIONS, IMAGE_TYPES, MAX_FILE_SIZE, detectCategory, isAcceptedFile, isImageFile, readFileAsBase64 } from "@/components/composer";
import type { TodayBeforeYouTalk, TodayPanelAnswer } from "@/lib/wails-shim-app";
import { computerClock } from "@/lib/brief-time";

type Translate = (key: string, options?: Record<string, unknown>) => string;

export function splitMeetingPrep(text: string): Record<"who" | "last" | "open" | "asked", string> {
    const result = { who: "", last: "", open: "", asked: "" };
    const headings: Record<string, keyof typeof result> = {
        "who's coming": "who", "who is coming": "who", "参会的人": "who",
        "last time": "last", "上次": "last", "still open": "open", "还没解决的": "open",
        "what they asked": "asked", "他们问过的": "asked",
    };
    let current: keyof typeof result | null = null;
    for (const line of text.split("\n")) {
        const stripped = line.trim().replace(/^#{1,6}\s*/, "").replace(/^\*\*(.*?)\*\*/, "$1");
        const heading = stripped.match(/^([^:：]+?)(?:\s*[:：]\s*(.*))?$/);
        const section = heading ? headings[heading[1].trim().toLocaleLowerCase()] : undefined;
        if (section) { current = section; if (heading?.[2]) result[section] = heading[2].trim(); continue; }
        if (current) result[current] = [result[current], line].filter(Boolean).join("\n").trim();
    }
    return result;
}

/** A main chat row as the panel draws it. Stream rows carry no time, so the
 *  row shows none (TopicRow shows no time for NaN). */
function toRow(item: StreamItem): TopicChatItem {
    return {
        id: item.id,
        kind: "message",
        at: Number.NaN,
        message: { id: item.id, kind: item.kind, content: item.content, sortOrder: item.sortOrder ?? 0 },
    };
}

/**
 * The whole main chat beside Today while no topic is open (Claude Design,
 * 2026-10-22): the same rows as the main chat page, in time order, with the
 * box underneath. Isoform's cards are compact rows that open in place, one
 * at a time; its status rows fold into one row per day; nothing disappears.
 * It is a chat first: the box is always there, Isoform answers inside the
 * panel and the same answer is in the main chat. The header reads "Chat"
 * with how many cards wait, and has no Pin. When the topics read
 * failed, one line at the top says so with Try again; the chat below keeps
 * working.
 */
export function MainChatPanel({
    variant = "pinned",
    topicsFailed,
    topicsRefreshing = false,
    topicsGathering = false,
    onRetryTopics,
    draft,
    prepared,
    contextRef = null,
    preview,
    topicNames,
    onDraftChange,
    handlers,
    onOpenReportTarget,
    starterRequest,
    savedPanel,
    meetingPrep,
    onRefreshSaved,
    onRetrySaved,
    onLeaveSaved,
    closeItemRequest,
    onItemOpenChange,
    onStarterConsumed,
    onClose,
    sharedSession,
    onLoadSharedMore,
    onHydrateShared,
    hideRoutine = false,
}: {
    /** Pinned beside the page, or in the Sheet on a narrow window. */
    variant?: "pinned" | "sheet";
    sharedSession?: import('@/types').Session;
    onLoadSharedMore?: () => Promise<void>;
    onHydrateShared?: (sessionId: string, items: StreamItem[], hasMore: boolean, cursor: number) => void;
    hideRoutine?: boolean;
    /** The topics read failed; the line at the top says so. */
    topicsFailed: boolean;
    topicsRefreshing?: boolean;
    topicsGathering?: boolean;
    /** The same read the Your topics grid retries. */
    onRetryTopics: () => void;
    /** The box's words; Today keeps them so a click elsewhere can prepare them. */
    draft: string;
    /** The words were put there by a click (an answer, a chip), not typed. */
    prepared: boolean;
    /** The Today item an answer was prepared from; it goes with the send. */
    contextRef?: TodayChatContextRef | null;
    /** The context line for that item: what Isoform adds, or why not. */
    preview?: TodayChatPreview;
    /** Topic names for the tags on Isoform's cards. */
    topicNames?: ReadonlyMap<string, string>;
    onDraftChange: (draft: string, prepared: boolean) => void;
    handlers: TodayActionHandlers;
    /** Opens what one of Isoform's cards points at, as the main chat page does. */
    onOpenReportTarget?: (target: MainChatReportTarget) => void;
    starterRequest?: { text: string; id: number; context?: TodayChatContextRef; firstChip?: boolean } | null;
    savedPanel?: { status: "loading" | "ready" | "failed"; text?: string; answer?: TodayPanelAnswer } | null;
    meetingPrep?: { meeting: TodayBeforeYouTalk; text: string } | null;
    onRefreshSaved?: () => void;
    onRetrySaved?: () => void;
    onLeaveSaved?: () => void;
    closeItemRequest?: number;
    onItemOpenChange?: (open: boolean) => void;
    onStarterConsumed?: (id: number) => void;
    onClose?: () => void;
}) {
    const showTopicsRefreshing = useTopicRefreshIndicator(topicsRefreshing && !topicsFailed);
    const { t, i18n } = useTranslation("today");
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const itemPanel = useMainChatItemPanel("today");
    const chat = useMainChatPanel(true);
    useEffect(() => {
        if (sharedSession && !sharedSession.itemsLoaded && chat.status === 'ready' && sharedSession.localId === chat.sessionId && typeof chat.cursor === 'number') {
            onHydrateShared?.(chat.sessionId, chat.items, chat.hasMore, chat.cursor);
        }
    }, [sharedSession, chat.status, chat.sessionId, chat.items, chat.hasMore, chat.cursor, onHydrateShared]);
    const shared = sharedSession?.itemsLoaded && sharedSession.localId === chat.sessionId ? sharedSession : undefined;
    // Keep the panel's in-flight rows visible while the shared session receives
    // the corresponding agent events. Older history still comes from the page.
    const displayedItems = useMemo(() => {
        if (!shared) return chat.items;
        if (!chat.waiting && !chat.pending) return shared.items;
        const live = new Map(chat.items.map((item) => [item.id, item]));
        const seen = new Set(shared.items.map((item) => item.id));
        return [...shared.items.map((item) => live.get(item.id) ?? item), ...chat.items.filter((item) => !seen.has(item.id))];
    }, [shared, chat.items, chat.waiting, chat.pending]);
    const hasMore = shared ? shared.hasMore : chat.hasMore;
    const loadingMore = shared ? shared.loadingMore : chat.loadingMore;
    const loadMore = shared && onLoadSharedMore ? onLoadSharedMore : chat.loadMore;
    const consumedStarter = useRef<number | null>(null);
    const [activeStarter, setActiveStarter] = useState<typeof starterRequest>(null);
    const composerInput = useRef<HTMLTextAreaElement>(null);
    useEffect(() => {
        if (!starterRequest || consumedStarter.current === starterRequest.id) return;
        consumedStarter.current = starterRequest.id;
        setActiveStarter(starterRequest);
        itemPanel.close();
        onStarterConsumed?.(starterRequest.id);
        void (starterRequest.firstChip ? chat.send(starterRequest.text, starterRequest.context, true, true) : chat.send(starterRequest.text, starterRequest.context, true));
    }, [starterRequest, onStarterConsumed, chat.send, itemPanel.close]);
    // Match the ordered list shown in Tasks and the menu bar.
    const waitingCount = useTasksNeedsYouCount();
    const chips = useMainChatAnswerChips(chat.status === "ready", answerChipIds(displayedItems));
    const listRef = useRef<HTMLDivElement>(null);
    const navigation = useReplyNavigation(displayedItems, listRef, { streaming: chat.waiting });
    const mounted = useRef(true);
    useEffect(() => {
        mounted.current = true;
        return () => { mounted.current = false; };
    }, []);
    const openTarget = onOpenReportTarget ?? handlers.onOpenReportTarget;

    // Follow the newest row as replies arrive; earlier rows shown above keep
    // the reader where they were.
    const lastRow = displayedItems[displayedItems.length - 1]?.id ?? "";
    useEffect(() => {
        const list = listRef.current;
        if (list && !navigation.userScrolledRef.current) list.scrollTop = list.scrollHeight;
    }, [lastRow, chat.pending?.state, chat.waiting]);

    // Only one card is open at a time; the full card never shows closed.
    const today = localDayKey(new Date());
    const names = useMemo(() => topicNames ?? new Map<string, string>(), [topicNames]);
    // An answer chip is the person's reply: it posts at once, as on the Chat
    // page, and folds to "You answered: … · Change" (chat-review v1.0.3 #3).
    const chatSend = chat.send;
    const streamView = useMemo(() => ({
        surface: 'today' as const,
        today,
        hideRoutine,
        topicNames: names,
        chips,
        chipsSend: true,
        onFillChip: (text: string) => {
            const started = performance.now();
            return chatSend(text, null, false, false, true).then((result) => {
                recordTodayAction("T68", "ask", result === "sent" ? "sent" : "failed", started);
                return result === "sent";
            });
        },
    }), [today, names, chips, chatSend, hideRoutine]);

    // The same box as the Chat page (chat-review v1.0.3 #4): attach and voice.
    const voice = useContext(ComposerVoiceContext);
    const [images, setImages] = useState<ImageAttachment[]>([]);
    const [files, setFiles] = useState<FileAttachment[]>([]);
    const [attachError, setAttachError] = useState<string | undefined>();
    const draftRef = useRef(draft);
    draftRef.current = draft;
    const addFiles = async (list: FileList) => {
        const all = Array.from(list);
        const accepted = all.filter((file) => isAcceptedFile(file) && file.size <= MAX_FILE_SIZE);
        setAttachError(all.some((file) => !isAcceptedFile(file)) ? tr("mainChatPanel.attachUnsupported")
            : all.some((file) => file.size > MAX_FILE_SIZE) ? tr("mainChatPanel.attachTooLarge") : undefined);
        const newImages = await Promise.all(accepted.filter(isImageFile).map(readFileAsBase64));
        const newFiles = await Promise.all(accepted.filter((file) => !isImageFile(file)).map(async (file): Promise<FileAttachment> => {
            const image = await readFileAsBase64(file);
            return { id: `file-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`, fileName: file.name, fileSize: file.size, mediaType: file.type || "application/octet-stream", category: detectCategory(file), dataBase64: image.dataBase64 };
        }));
        if (!mounted.current) return;
        if (newImages.length) setImages((current) => [...current, ...newImages]);
        if (newFiles.length) setFiles((current) => [...current, ...newFiles]);
    };
    const toggleVoice = () => {
        if (!voice) return;
        if (voice.state === "recording") {
            void voice.stop().then((heard) => {
                const words = heard.trim();
                if (!words || !mounted.current) return;
                const current = draftRef.current;
                onDraftChange(current.trim() ? `${current} ${words}` : words, false);
            });
            return;
        }
        if (voice.needsPermission && voice.openSettings) {
            void voice.openSettings();
            return;
        }
        void voice.start();
    };

    const send = async (words = draft) => {
        const text = words.trim();
        if (!text || chat.pending?.state === "sending" || preview?.status === "loading") return;
        onLeaveSaved?.();
        setActiveStarter(null);
        onDraftChange("", false);
        const started = performance.now();
        let context = contextRef;
        if (contextRef?.telemetryAction && !prepared) {
            const { telemetryAction: _action, ...typedContext } = contextRef;
            context = typedContext;
        }
        const attached = { images, files };
        const withAttachments = !context && (images.length > 0 || files.length > 0);
        if (withAttachments) {
            setImages([]);
            setFiles([]);
        }
        const result = withAttachments ? await chat.send(text, context, false, false, false, attached) : await chat.send(text, context);
        recordTodayAction("T68", "ask", result === "sent" ? "sent" : "failed", started);
        // Not sent: the words and attachments go back in the box.
        if (result === "failed" && mounted.current) {
            onDraftChange(text, false);
            if (withAttachments) {
                setImages(attached.images);
                setFiles(attached.files);
            }
        }
    };
    const retryPending = async () => {
        const failed = chat.pending;
        if (!failed || failed.state !== "failed") return;
        const result = await (failed.answerChip ? chat.send(failed.text, failed.context, failed.ask, false, true) : failed.firstChip ? chat.send(failed.text, failed.context, failed.ask, true) : chat.send(failed.text, failed.context, failed.ask));
        if (result === "sent" && mounted.current && draft.trim() === failed.text) onDraftChange("", false);
        return result;
    };

    const Title = variant === "sheet" ? SheetTitle : "h2";
    const reverseIndex = meetingPrep && chat.lastUserItemId ? [...chat.items].reverse().findIndex((item) => item.id === chat.lastUserItemId && item.kind === "user") : -1;
    const meetingIndex = reverseIndex < 0 ? -1 : chat.items.length - 1 - reverseIndex;
    const meetingReply = meetingIndex >= 0 ? chat.items.slice(meetingIndex + 1).reverse().find((item) => item.kind === "assistant" && !!item.content?.trim()) : undefined;
    const prepText = savedPanel?.answer?.answer ?? meetingReply?.content ?? "";
    const prepSections = splitMeetingPrep(prepText);
    const prepRunning = !!meetingPrep && (savedPanel?.status === "loading" || !!starterRequest || chat.pending?.state === "sending" || chat.waiting);
    const prepMadeAt = savedPanel?.answer?.madeAt ?? meetingReply?.createdAt;
    const sourceIndex = !meetingPrep && activeStarter?.context && chat.lastUserItemId
        ? chat.items.findIndex((item) => item.id === chat.lastUserItemId && item.kind === "user") : -1;
    const sourceAnswer = sourceIndex < 0 ? undefined : chat.items.slice(sourceIndex + 1).find((item) => item.kind === "assistant" && !!item.content?.trim());
    const sourceDone = !!sourceAnswer && !chat.waiting && !chat.pending && !chat.stopped && !starterRequest &&
        (activeStarter?.context?.sourceKind === "brief_card" || activeStarter?.context?.sourceKind === "brief");
    const answerActions = (refresh: () => void) => <div className="flex flex-wrap items-center gap-2 text-[12px] text-muted" data-testid="brief-answer-actions">
        <span>{tr("mainChatPanel.fromBrief")}</span>
        <Button size="sm" variant="quiet" onClick={() => measureToday("T67", "do", "done", refresh)}>{tr("mainChatPanel.refresh")}</Button>
        {handlers.onOpenMainChat && <Button size="sm" variant="quiet" onClick={() => measureToday("T67", "open", "opened", () => handlers.onOpenMainChat?.())}>{tr("mainChatPanel.openInChat")}</Button>}
    </div>;
    const itemOpen = !itemPanel.openId && (!!meetingPrep || !!savedPanel || !!activeStarter?.context);
    const leaveItem = () => { setActiveStarter(null); onLeaveSaved?.(); };
    useEffect(() => { onItemOpenChange?.(itemOpen); return () => onItemOpenChange?.(false); }, [itemOpen, onItemOpenChange]);
    const previousCloseRequest = useRef(closeItemRequest);
    useEffect(() => {
        if (previousCloseRequest.current !== closeItemRequest) {
            previousCloseRequest.current = closeItemRequest;
            setActiveStarter(null);
        }
    }, [closeItemRequest]);
    useEffect(() => {
        if (!itemOpen) return;
        const close = (event: globalThis.KeyboardEvent) => {
            if (event.key === "Escape" && !event.defaultPrevented) { event.preventDefault(); leaveItem(); }
        };
        document.addEventListener("keydown", close);
        return () => document.removeEventListener("keydown", close);
    }, [itemOpen, onLeaveSaved]);
    if (itemPanel.openId) {
        return <MainChatItemPanel surface="today" onOpenTarget={openTarget} />;
    }
    return (
        <aside
            aria-label={tr("mainChatPanel.title")}
            className={cn("flex h-full min-h-0 flex-col bg-background-accent", variant === "pinned" && "border-l border-border-subtle")}
            data-testid="main-chat-panel"
        >
            <div className="flex shrink-0 flex-col gap-1 border-b border-border-subtle px-5 pb-3.5 pt-[18px]">
                <div className="flex items-center gap-2">
                    <Title className="text-[18px] font-semibold leading-tight text-ink">{meetingPrep ? tr("meetingPrep.title", { time: computerClock(new Date(meetingPrep.meeting.startAt), i18n.language), name: meetingPrep.meeting.personName }) : savedPanel?.answer?.question ?? savedPanel?.text ?? activeStarter?.text ?? tr("mainChatPanel.title")}</Title>
                    {chat.waiting && chat.runId && (
                        <Button size="sm" variant="outline" className="ml-auto" onClick={() => void measureToday("T67", "do", "stopped", chat.stop)}>{tr("mainChatPanel.stop")}</Button>
                    )}
                    {waitingCount != null && waitingCount > 0 && (
                        <Button
                            size="sm"
                            variant="outline"
                            className="ml-auto h-7 rounded-full border-warning-border bg-warning-bg px-2.5 text-[12px] text-warning hover:bg-warning-bg hover:text-warning"
                            onClick={() => measureToday("T69", "open", "opened", () => handlers.onOpenTasks?.())}
                            data-testid="main-chat-panel-waiting"
                        >
                            {tr("mainChatPanel.waiting", { count: waitingCount })}
                        </Button>
                    )}
                    {itemOpen && <Button size="icon" variant="quiet" className="ml-auto" aria-label={tr("topicPanel.close")} onClick={leaveItem}><X className="size-4" /></Button>}
                    {!itemOpen && variant === "sheet" && (
                        <SheetClose asChild>
                            <Button size="icon" variant="quiet" className={cn((waitingCount == null || waitingCount === 0) && "ml-auto")} aria-label={tr("topicPanel.close")} onClick={() => measureToday("T67", "open", "opened", () => undefined)}>
                                <X className="size-4" />
                            </Button>
                        </SheetClose>
                    )}
                    {!itemOpen && variant === "pinned" && onClose && (
                        <Button size="icon" variant="quiet" className="ml-auto" aria-label={tr("topicPanel.close")} onClick={() => measureToday("T67", "open", "opened", () => onClose?.())}>
                            <X className="size-4" />
                        </Button>
                    )}
                </div>
                {showTopicsRefreshing && (
                    <p role="status" className="text-[12.5px] text-muted" data-testid="main-chat-panel-topics-refreshing">{tr("yourTopics.refreshing")}</p>
                )}
                {topicsGathering && !topicsFailed && (
                    <p role="status" className="text-[12.5px] text-muted" data-testid="main-chat-panel-topics-gathering">
                        {tr("yourTopics.gathering")}
                    </p>
                )}
                {topicsFailed && (
                    <div role="alert" className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[12.5px] text-ink" data-testid="main-chat-panel-topics-failed">
                        <AlertCircle className="size-[15px] shrink-0 text-error" />
                        <span>{tr("yourTopics.loadFailed")}</span>
                        <Button size="sm" variant="quiet" className="h-6" onClick={() => measureToday("T51", "do", "done", () => onRetryTopics?.())}>{tr("todayBrief.retry")}</Button>
                    </div>
                )}
            </div>

            <div className="flex min-h-0 flex-1 flex-col">
                <MainChatViewContext.Provider value={streamView}>
                    <div ref={listRef} className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4 py-4" data-testid="main-chat-panel-list">
                        {meetingPrep ? <>
                            {(["who", "last", "open", "asked"] as const).map((section) => (prepRunning || prepSections[section]) && <section key={section} className="flex flex-col gap-1.5" data-testid={`meeting-prep-${section}`}>
                                <h3 className="text-[12px] font-semibold text-muted">{tr(`meetingPrep.${section}`)}</h3>
                                {prepSections[section] ? <p className="whitespace-pre-wrap text-[13.5px] text-ink">{prepSections[section]}</p> : <span className="h-2.5 w-3/4 rounded-md bg-surface-active" />}
                            </section>)}
                            {!prepRunning && prepText && !Object.values(prepSections).some(Boolean) && <p className="whitespace-pre-wrap text-[13.5px] text-ink">{prepText}</p>}
                            {savedPanel?.status === "failed" && <p role="alert" className="text-[13px] text-error">{tr("meetingPrep.failed")} <Button size="sm" variant="quiet" onClick={() => measureToday("T67", "do", "done", () => onRetrySaved?.())}>{tr("todayBrief.retry")}</Button></p>}
                            {chat.pending?.state === "failed" && <p role="alert" className="text-[13px] text-error">{tr("topicPanel.notSent")} <Button size="sm" variant="quiet" onClick={() => void measureToday("T68", "ask", "sent", retryPending)}>{tr("todayBrief.retry")}</Button></p>}
                            {chat.stopped && <p role="status" className="text-[13px] text-muted">{tr("mainChatPanel.stopped")} <Button size="sm" variant="quiet" onClick={() => void measureToday("T67", "ask", "sent", chat.startOver)}>{tr("mainChatPanel.startOver")}</Button></p>}
                            {!prepRunning && prepText && <div className="flex items-center gap-2 text-[12px] text-muted">
                                {prepMadeAt && <span>{tr("meetingPrep.made", { time: computerClock(new Date(prepMadeAt), i18n.language) })}</span>}
                                <Button size="sm" variant="quiet" onClick={() => measureToday("T67", "do", "done", () => onRefreshSaved?.())}>{tr("mainChatPanel.refresh")}</Button>
                                {handlers.onOpenMainChat && <Button size="sm" variant="quiet" onClick={() => measureToday("T67", "open", "opened", () => handlers.onOpenMainChat?.())}>{tr("mainChatPanel.openMainChat")}</Button>}
                            </div>}
                        </> : savedPanel ? <>
                            {savedPanel.status === "loading" && <p role="status" className="text-[13px] text-muted">{tr("topicPanel.working")}</p>}
                            {savedPanel.status === "failed" && <p role="alert" className="flex items-center gap-2 text-[13px] text-error">{tr("mainChatPanel.loadFailed")}
                                <Button size="sm" variant="quiet" onClick={() => measureToday("T67", "do", "done", () => onRetrySaved?.())}>{tr("todayBrief.retry")}</Button></p>}
                            {savedPanel.status === "ready" && savedPanel.answer && <>
                                <TopicRow item={toRow({ id: savedPanel.answer.userItemId, kind: "user", content: savedPanel.answer.question })} language={i18n.language} tr={tr} />
                                <TopicRow item={toRow({ id: savedPanel.answer.answerItemId, kind: "assistant", content: savedPanel.answer.answer })} language={i18n.language} tr={tr} />
                                {answerActions(() => onRefreshSaved?.())}
                            </>}
                        </> : <>
                        {chat.status === "loading" && (
                            <div aria-busy="true" className="flex flex-col gap-2.5" data-testid="main-chat-panel-loading">
                                <span className="h-2.5 w-3/5 rounded-md bg-surface-active" />
                                <span className="h-3 w-[90%] rounded-md bg-surface-active" />
                                <span className="h-3 w-3/4 rounded-md bg-surface-active" />
                                <span className="h-2.5 w-2/5 rounded-md bg-surface-active" />
                            </div>
                        )}
                        {chat.status === "failed" && (
                            <div className="flex flex-col gap-2.5" data-testid="main-chat-panel-failed">
                                <p role="alert" className="flex items-start gap-2 text-[13.5px] text-ink">
                                    <AlertCircle className="mt-0.5 size-[15px] shrink-0 text-error" />
                                    {tr("mainChatPanel.loadFailed")}
                                </p>
                                <div className="flex flex-wrap gap-2">
                                    <Button size="sm" variant="secondary" onClick={() => void measureToday("T67", "do", "done", chat.reload)}>{tr("todayBrief.retry")}</Button>
                                    {handlers.onOpenMainChat && (
                                        <Button size="sm" variant="quiet" onClick={() => measureToday("T67", "open", "opened", () => handlers.onOpenMainChat?.())}>{tr("mainChatPanel.openMainChat")}</Button>
                                    )}
                                </div>
                            </div>
                        )}
                        {chat.status === "ready" && hasMore && (
                            <div>
                                <Button size="sm" variant="quiet" disabled={loadingMore} aria-busy={loadingMore} onClick={() => void measureToday("T67", "open", "opened", loadMore)}>
                                    {loadingMore && <Loader2 className="size-3.5 animate-spin" />}
                                    {tr("topicPanel.earlier")}
                                </Button>
                            </div>
                        )}
                        {chat.status === "ready" && displayedItems.length === 0 && !hasMore && !chat.pending && !chat.waiting && (
                            <p className="text-[14px] text-ink" data-testid="main-chat-panel-empty">{tr("mainChatPanel.empty")}</p>
                        )}
                        <GroupedStreamList items={displayedItems} streaming={chat.waiting} onOpenReportTarget={openTarget} />
                        {chat.pending && (
                            <div className="flex flex-col items-end gap-1.5" data-testid="main-chat-panel-pending">
                                <div className={cn("max-w-[85%] whitespace-pre-wrap rounded-xl border border-border bg-panel-user px-[13px] py-[9px] text-[14px] text-ink", chat.pending.state === "sending" && "opacity-70")}>
                                    {chat.pending.text}
                                </div>
                                {chat.pending.state === "failed" && (
                                    <p role="alert" className="flex items-center gap-1.5 text-[12.5px] text-error">
                                        {tr("topicPanel.notSent")}
                                        <Button size="sm" variant="quiet" className="h-6" onClick={() => void measureToday("T68", "ask", "sent", retryPending)}>
                                            {tr("todayBrief.retry")}
                                        </Button>
                                    </p>
                                )}
                            </div>
                        )}
                        {(chat.pending?.state === "sending" || chat.waiting) && (
                            <p role="status" className="flex items-center gap-2 text-[13.5px] text-muted">
                                <span aria-hidden className="flex gap-1 animate-pulse motion-reduce:animate-none">
                                    <span className="size-1 rounded-full bg-muted" /><span className="size-1 rounded-full bg-muted" /><span className="size-1 rounded-full bg-muted" />
                                </span>
                                {tr("topicPanel.working")}
                            </p>
                        )}
                        {sourceDone && activeStarter && answerActions(() => void (activeStarter.firstChip ? chat.send(activeStarter.text, activeStarter.context, true, true) : chat.send(activeStarter.text, activeStarter.context, true)))}
                        {chat.stopped && <p className="flex items-center gap-2 text-[12.5px] text-muted" role="status">
                            {tr("mainChatPanel.stopped")}
                            <Button size="sm" variant="quiet" disabled={chat.waiting} onClick={() => void measureToday("T67", "ask", "sent", chat.startOver)}>{tr("mainChatPanel.startOver")}</Button>
                        </p>}
                        </>}
                    </div>
                </MainChatViewContext.Provider>
            </div>

            <TopicComposer
                inputRef={composerInput}
                replyNavigation={navigation}
                contextPreview={<TodayContextPreview preview={preview} />}
                className="px-4 pb-4 pt-3"
                draft={draft}
                disabled={preview?.status === "loading"}
                prepared={prepared}
                onDraftChange={onDraftChange}
                onSend={() => void send()}
                sending={chat.pending?.state === "sending"}
                inputLabel={tr("mainChatPanel.inputLabel")}
                placeholder={tr("mainChatPanel.placeholder")}
                sendLabel={tr("topicPanel.send")}
                note={prepared && contextRef ? tr("topicPanel.notePrepared") : prepared ? tr("mainChatPanel.notePrepared") : undefined}
                attach={{
                    label: tr("mainChatPanel.attach"),
                    accept: [...IMAGE_TYPES, ...ACCEPTED_FILE_EXTENSIONS].join(","),
                    onFiles: (list) => void addFiles(list),
                    error: attachError,
                    items: [
                        ...images.map((image, index) => ({ key: `image-${index}`, name: image.fileName ?? tr("mainChatPanel.attach"), removeLabel: tr("mainChatPanel.removeAttachment", { file: image.fileName ?? "" }), onRemove: () => setImages((current) => current.filter((_, i) => i !== index)) })),
                        ...files.map((file) => ({ key: file.id, name: file.fileName, removeLabel: tr("mainChatPanel.removeAttachment", { file: file.fileName }), onRemove: () => setFiles((current) => current.filter((other) => other.id !== file.id)) })),
                    ],
                }}
                voice={voice ? { label: tr("mainChatPanel.voice"), stopLabel: tr("mainChatPanel.voiceStop"), state: voice.state, onToggle: toggleVoice } : undefined}
            />
        </aside>
    );
}
