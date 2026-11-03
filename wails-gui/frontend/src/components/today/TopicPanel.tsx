import { measureToday, recordTodayAction } from "@/lib/today-telemetry";
import { memo, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, Loader2, X } from "lucide-react";
import { useReplyNavigation } from "@/hooks/useReplyNavigation";
import { Button } from "@/components/ui/button";
import { SheetClose, SheetTitle } from "@/components/ui/sheet";
import { DigestMarkdown } from "@/components/digest-markdown";
import { cn } from "@/lib/utils";
import { GetTodayObservationPauseSince } from "@/lib/wails-shim-app";
import { clockTime, computerClock } from "@/lib/brief-time";
import { useTopicChat } from "@/hooks/useTopicChat";
import { TopicComposer } from "./TopicComposer";
import type { TodayActionHandlers } from "./types";
import { isDisplayTopic, type TodayTopic, type TopicChatItem } from "./topics";

type Translate = (key: string, options?: Record<string, unknown>) => string;

const STATUS_TONE: Record<NonNullable<TodayTopic["status"]>, string> = {
    waiting: "border-warning-border bg-warning-bg text-warning",
    waitingOnThem: "border-border bg-panel-warm text-muted",
    today: "border-border bg-panel-warm text-muted",
    moving: "border-border bg-panel-warm text-muted",
    news: "border-info-border bg-info-bg text-info",
};

/** A topic's state as a small chip: "Waiting on you", "Moving", "2 new". */
export function TopicStatusChip({ topic, tr }: { topic: TodayTopic; tr: Translate }) {
    if (!topic.status) return null;
    return (
        <span className={cn("inline-flex min-h-5 max-w-full items-center whitespace-normal [overflow-wrap:anywhere] rounded-full border px-[7px] text-[11px] font-medium", STATUS_TONE[topic.status])}>
            {topic.status === "news" && !topic.newCount
                ? tr("topicPanel.status.newStories")
                : tr(`topicPanel.status.${topic.status}`, { count: topic.newCount ?? 0 })}
        </span>
    );
}

/** "Dana, Lee and 2 others": up to three names and how many in all. */
export function peopleLine(topic: TodayTopic, tr: Translate): string | null {
    const people = topic.people;
    if (!people || people.names.length === 0) return null;
    const names = people.names.join(", ");
    const others = people.count - people.names.length;
    return others > 0 ? tr("topicPanel.peopleMore", { names, count: others }) : names;
}

/** When a row happened: the time today (in the one clock), else the day. */
export function rowTime(at: number, language: string, tr: Translate): string {
    const when = new Date(at);
    if (Number.isNaN(when.getTime())) return "";
    const today = new Date();
    const sameDay = when.toDateString() === today.toDateString();
    return sameDay
        ? clockTime(when, language, tr)
        : when.toLocaleDateString(language, { month: "short", day: "numeric" });
}

/**
 * The topic panel on Today (RC 1): the main chat filtered to one topic, with
 * its Activity events, "Show everything", and a box that sends to the main
 * chat with the topic attached. It is never a side chat: what is sent here
 * is in the main chat too. Pinned beside the page on wide windows
 * ("pinned"); in a Sheet from the right edge on narrow ones ("sheet").
 */
export const TopicPanel = memo(function TopicPanel({
    topic,
    variant,
    draft,
    prepared,
    onDraftChange,
    handlers,
    observationPaused,
    onTogglePin,
}: {
    topic: TodayTopic;
    variant: "pinned" | "sheet";
    /** Invalidates memoized time labels when the parent observes a day/zone change. */
    clockContext?: string;
    /** The box's words; Today keeps them so a click elsewhere can prepare them. */
    draft: string;
    /** The words were put there by a click on Today, not typed. */
    prepared: boolean;
    onDraftChange: (draft: string, prepared: boolean) => void;
    handlers: TodayActionHandlers;
    observationPaused: boolean | null;
    /** Pins or unpins the topic in the sidebar; resolves false when it could
     *  not. Absent until the topic registry can pin. */
    onTogglePin?: () => Promise<boolean>;
}) {
    const { t, i18n } = useTranslation("today");
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const chat = useTopicChat(topic.id);
    const listRef = useRef<HTMLDivElement>(null);
    const paused = observationPaused === true;
    const [pauseSince, setPauseSince] = useState(0);
    useEffect(() => {
        let active = true;
        setPauseSince(0);
        if (paused) void GetTodayObservationPauseSince().then((value) => { if (active) setPauseSince(value); }).catch(() => {});
        return () => { active = false; };
    }, [paused]);
    const navigation = useReplyNavigation(chat.items, listRef, { streaming: chat.waiting });
    const mounted = useRef(true);
    useEffect(() => {
        // StrictMode mounts twice in development; mark it mounted again.
        mounted.current = true;
        return () => { mounted.current = false; };
    }, []);
    const [pinState, setPinState] = useState<"idle" | "busy" | "failed">("idle");
    const togglePin = async () => {
        if (!onTogglePin || pinState === "busy") return;
        const started = performance.now();
        setPinState("busy");
        try {
            const done = await onTogglePin();
            recordTodayAction("T60", "do", done ? "done" : "failed", started);
            if (mounted.current) setPinState(done ? "idle" : "failed");
        } catch {
            recordTodayAction("T60", "do", "failed", started);
            if (mounted.current) setPinState("failed");
        }
    };
    const people = peopleLine(topic, tr);

    // Follow the newest row as replies arrive; earlier rows shown above keep
    // the reader where they were.
    const lastRow = chat.items[chat.items.length - 1]?.id ?? "";
    useEffect(() => {
        const list = listRef.current;
        if (list && !navigation.userScrolledRef.current) list.scrollTop = list.scrollHeight;
    }, [lastRow, chat.pending?.state, chat.waiting]);

    const send = async (words = draft) => {
        const text = words.trim();
        if (!text || chat.pending?.state === "sending") return;
        onDraftChange("", false);
        const started = performance.now();
        const result = await chat.send(text);
        recordTodayAction("T63", "ask", result === "sent" ? "sent" : "failed", started);
        // Not sent: the words go back in the box, unless the panel has
        // moved to another topic since.
        if (result === "failed" && mounted.current) onDraftChange(text, false);
    };

    const Title = variant === "sheet" ? SheetTitle : "h2";
    const pauseTime = pauseSince > 0 ? computerClock(new Date(pauseSince), i18n.resolvedLanguage) : "";
    const note = paused ? (pauseTime ? tr("topicPanel.pausedNote", { time: pauseTime }) : tr("captured.pausedUnknown")) : prepared ? tr("topicPanel.notePrepared") : tr("topicPanel.noteTopic");

    if (!isDisplayTopic(topic)) return null;

    return (
        <aside
            aria-label={tr("topicPanel.label", { name: topic.name })}
            className={cn("flex min-h-0 min-w-0 flex-col [overflow-wrap:anywhere] bg-background-accent", variant === "pinned" ? "h-full border-l border-border-subtle" : "h-full")}
            data-testid="topic-panel"
        >
            <div className="flex shrink-0 flex-col gap-2 border-b border-border-subtle px-5 pb-3.5 pt-[18px]">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <span className="text-[11px] font-medium uppercase tracking-[0.08em] text-muted">
                        {tr(`topicPanel.kind.${topic.kind}`)}
                    </span>
                    <TopicStatusChip topic={topic} tr={tr} />
                    <span className="ml-auto flex items-center gap-1">
                        {onTogglePin && chat.status !== "withdrawn" && (
                            <Button
                                size="sm"
                                variant="quiet"
                                aria-busy={pinState === "busy"}
                                disabled={pinState === "busy"}
                                onClick={() => void togglePin()}
                                data-testid="topic-panel-pin"
                            >
                                {pinState === "busy" && <Loader2 className="size-3.5 animate-spin" />}
                                {tr(topic.pinned ? "topicPanel.pinned" : "topicPanel.pin")}
                            </Button>
                        )}
                        {variant === "sheet" && (
                            <SheetClose asChild>
                                <Button size="icon" variant="quiet" aria-label={tr("topicPanel.close")} onClick={() => measureToday("T61", "open", "opened", () => undefined)}>
                                    <X className="size-4" />
                                </Button>
                            </SheetClose>
                        )}
                    </span>
                </div>
                <Title className="text-[18px] font-semibold leading-tight text-ink">{topic.name}</Title>
                {topic.latestFind && <p className="text-[12.5px] text-muted">{tr("yourTopics.newFind", { headline: topic.latestFind.text })}</p>}
                {people && <p className="text-[12.5px] text-muted">{people}</p>}
                {pinState === "failed" && (
                    <p role="alert" className="text-[12.5px] text-error">{tr("topicPanel.pinFailed")}</p>
                )}
            </div>

            <div className="flex min-h-0 min-w-0 flex-1 flex-col">
                <div ref={listRef} className="flex min-h-0 min-w-0 flex-1 flex-col gap-3.5 overflow-y-auto px-5 py-4 [&>*]:shrink-0" data-testid="topic-panel-list">
                    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-lg bg-panel-warm px-3 py-2 text-[12.5px] text-muted">
                        <span className="min-w-0 max-w-full">{tr("topicPanel.showingOnly", { name: topic.name })}</span>
                        {handlers.onOpenMainChat && (
                            <Button variant="link" className="h-auto p-0 text-[12.5px] font-medium" onClick={() => measureToday("T61", "open", "opened", () => handlers.onOpenMainChat?.())}>
                                {tr("topicPanel.openInChat")}
                            </Button>
                        )}
                    </div>

                    {chat.status === "loading" && (
                        <div aria-busy="true" className="flex flex-col gap-2.5" data-testid="topic-panel-loading">
                            <span className="h-2.5 w-3/5 rounded-md bg-surface-active" />
                            <span className="h-3 w-[90%] rounded-md bg-surface-active" />
                            <span className="h-3 w-3/4 rounded-md bg-surface-active" />
                            <span className="h-2.5 w-2/5 rounded-md bg-surface-active" />
                        </div>
                    )}
                    {chat.status === "failed" && (
                        <div className="flex flex-col gap-2.5" data-testid="topic-panel-failed">
                            <p role="alert" className="flex items-start gap-2 text-[13.5px] text-ink">
                                <AlertCircle className="mt-0.5 size-[15px] shrink-0 text-error" />
                                {tr("topicPanel.loadFailed")}
                            </p>
                            <div className="flex flex-wrap gap-2">
                                <Button size="sm" variant="secondary" onClick={() => void measureToday("T62", "do", "done", chat.reload)}>{tr("todayBrief.retry")}</Button>
                                {handlers.onOpenMainChat && (
                                    <Button size="sm" variant="quiet" onClick={() => measureToday("T61", "open", "opened", () => handlers.onOpenMainChat?.())}>{tr("topicPanel.openInChat")}</Button>
                                )}
                            </div>
                        </div>
                    )}
                    {chat.status === "withdrawn" && (
                        <div className="flex flex-col gap-2.5" data-testid="topic-panel-withdrawn">
                            <p role="alert" className="flex items-start gap-2 text-[13.5px] text-ink">
                                <AlertCircle className="mt-0.5 size-[15px] shrink-0 text-error" />
                                {tr("topicPanel.withdrawn")}
                            </p>
                            {handlers.onOpenMainChat && (
                                <div>
                                    <Button size="sm" variant="quiet" onClick={() => measureToday("T61", "open", "opened", () => handlers.onOpenMainChat?.())}>{tr("topicPanel.openInChat")}</Button>
                                </div>
                            )}
                        </div>
                    )}
                    {chat.status === "ready" && chat.hasMore && (
                        <div>
                            <Button size="sm" variant="quiet" disabled={chat.loadingMore} aria-busy={chat.loadingMore} onClick={() => void measureToday("T62", "open", "opened", chat.loadMore)}>
                                {chat.loadingMore && <Loader2 className="size-3.5 animate-spin" />}
                                {tr("topicPanel.earlier")}
                            </Button>
                        </div>
                    )}
                    {chat.status === "ready" && chat.items.length === 0 && !chat.hasMore && !chat.pending && !chat.waiting && (
                        <p className="text-[14px] text-ink" data-testid="topic-panel-empty">{tr("topicPanel.empty", { name: topic.name })}</p>
                    )}
                    {chat.items.map((item) => (
                        <TopicRow key={item.id} item={item} language={i18n.language} tr={tr} />
                    ))}
                    {chat.pending && (
                        <div className="flex flex-col items-end gap-1.5" data-testid="topic-panel-pending">
                            <div className={cn("min-w-0 max-w-[85%] [overflow-wrap:anywhere] whitespace-pre-wrap rounded-xl border border-border bg-panel-user px-[13px] py-[9px] text-[14px] text-ink", chat.pending.state === "sending" && "opacity-70")}>
                                {chat.pending.text}
                            </div>
                            {chat.pending.state === "failed" && (
                                <p role="alert" className="flex items-center gap-1.5 text-[12.5px] text-error">
                                    {tr("topicPanel.notSent")}
                                    <Button size="sm" variant="quiet" className="h-6" onClick={() => void send(chat.pending?.text)}>
                                        {tr("todayBrief.retry")}
                                    </Button>
                                </p>
                            )}
                        </div>
                    )}
                    {(chat.pending?.state === "sending" || chat.waiting) && (
                        <p role="status" className="flex items-center gap-2 text-[13.5px] text-muted">
                            <Loader2 className="size-3.5 animate-spin" />
                            {tr("topicPanel.working")}
                        </p>
                    )}
                </div>

            </div>

            {chat.status !== "withdrawn" && (
                <TopicComposer
                    replyNavigation={navigation}
                    className="px-4 pb-4 pt-3"
                    draft={draft}
                    prepared={prepared}
                    onDraftChange={onDraftChange}
                    onSend={() => void send()}
                    sending={chat.pending?.state === "sending"}
                    inputLabel={tr("topicPanel.inputLabel")}
                    placeholder={tr("topicPanel.placeholder", { name: topic.name })}
                    sendLabel={tr("topicPanel.send")}
                    note={note}
                />
            )}
        </aside>
    );
});

/** One row: an Activity event ("You reviewed v0.3 comments"), the user's own
 *  words, or Isoform's reply. Tool, thinking and system rows stay in the
 *  main chat. */
export function TopicRow({ item, language, tr }: { item: TopicChatItem; language: string; tr: Translate }) {
    const time = rowTime(item.at, language, tr);
    if (item.kind === "event") {
        if (!item.text?.trim()) return null;
        return (
            <div className="flex min-w-0 max-w-full items-start gap-2.5 text-[12.5px] text-muted" data-testid="topic-panel-event">
                <span aria-hidden className="mt-[5px] size-[7px] shrink-0 rounded-full bg-ink" />
                <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">
                    <span className="font-semibold text-ink">{tr("topicPanel.you")}</span> {item.text}
                </span>
                {time && <span className="shrink-0 tabular-nums">{time}</span>}
            </div>
        );
    }
    const message = item.message;
    const content = message?.content?.trim();
    if (!message || !content) return null;
    if (message.kind === "user") {
        return (
            <div className="min-w-0 max-w-[85%] self-end whitespace-pre-wrap [overflow-wrap:anywhere] rounded-xl border border-border bg-panel-user px-[13px] py-[9px] text-[14px] text-ink" data-testid="topic-panel-user">
                {content}
            </div>
        );
    }
    if (message.kind !== "assistant") return null;
    return (
        <div className="flex min-w-0 max-w-full flex-col gap-1.5 [overflow-wrap:anywhere]" data-testid="topic-panel-isoform">
            <span className="text-[12px] text-accent">{time ? tr("topicPanel.isoformAt", { time }) : tr("topicPanel.isoform")}</span>
            <DigestMarkdown text={content} variant="compact" className="min-w-0 [&_pre]:whitespace-pre-wrap [&_table]:w-full [&_table]:table-fixed" />
        </div>
    );
}
