import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type Ref } from "react";
import { useTranslation } from "react-i18next";
import { ArrowLeft, Check, CheckCircle2, ChevronDown, Clock, FileText, MoreHorizontal, Moon, Sun, X } from "lucide-react";
import { handoffBlockedReasonKey } from "@/components/handoffs/handoffStatus";
import { AutomationAppIcon } from "@/components/automations/AutomationAppIcon";
import { createScheduleTextLabels } from "@/components/automations/schedule-utils";
import { upcomingName, upcomingNextLabel, upcomingSummary, upcomingWhen } from "@/components/automations/upcoming-text";
import { isSitesChanged } from "@/components/sops/PlaybookDeliveryPanel";
import { usePlaybookLibrary } from "@/components/playbooks/usePlaybookLibrary";
import { offerShowsNow } from "@/components/playbooks/playbook-lists";
import {
    AbortHandoff, AcceptHandoff, AcceptPlaybookSuggestion, AddBrainDump, ParseBrainDump, CancelOpenAnswer, ChangeOpenAnswer, SnoozePlaybookSuggestion,
    GetFirstWeekDay, ListDeliverables, OpenDeliverableFile, RevealDeliverableFile,
    ListDesktopTaskThreads, ActOnDesktopTask,
    type BrainDumpItem, type DesktopScheduledTask, type DesktopTaskThread, type DeliverableEntry, type DeliverableFileRef, type HandoffAcceptResult, type HandoffEntry, type PlaybookView, type SopEntry, type TodayNeed, type UpcomingItem,
} from "@/lib/wails-shim-app";
import { ListHandoffs, usePageQueryRefresh } from "@/lib/page-queries";
import { pageQueryCache } from "@/lib/query-cache";
import { cn } from "@/lib/utils";
import { markPageContent } from "@/lib/page-switch-timing";
import { putOffTodayNeed, useTodayNeeds } from "@/hooks/useTodayNeeds";
import type { HandoffStartOptions } from "@/hooks/useProjects";
import { useMainChatItemPanel } from "@/hooks/useMainChatItemPanel";
import { MainChatItemPanel } from "@/components/main-chat-item-panel";
import { getOpenAnswersGeneration, refreshOpenAnswers, updateOpenAnswer, useOpenAnswers } from "@/hooks/useOpenAnswers";
import { parseOpenAnswer, type OpenAnswerDelivery } from "@/lib/open-answers";
import { briefClockTime, computerClock, computerHourOptions } from "@/lib/brief-time";
import appI18n from "@/i18n";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { Button } from "@/components/ui/button";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "@/components/ui/context-menu";
import { SearchIcon } from "@/assets/icons/SearchIcon";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { NOT_NOW_UNDO_MS, TaskSuggestionDeck } from "./TaskSuggestionDeck";
import { TaskSectionHeader } from "./TaskSectionHeader";
import { TaskThreadPane } from "./TaskThreadPane";
import { BriefDetailPane, type BriefSlot } from "./BriefDetailPane";
import { briefEvery, briefNext } from "./brief-schedule-text";
import { clockTime } from "@/lib/brief-time";
import { undoTaskStart, usePendingTaskStarts } from "./pending-task-starts";
import { taskOutcomeLine } from "./task-outcome";
import { beginRestart, openYourAI, stopRowButton, stopRowLine, undoRestart, usePendingRestarts } from "./task-stop";
import { onPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";
import { todayReviewSentence } from "@/lib/today-review-sentence";
import { measureTasks, recordTasksAction, useTasksSectionSeen, type TasksActionId } from "@/lib/tasks-telemetry";
import { FailureLine } from "./FailureLine";

/** Where Tasks should scroll or switch to when it is opened from elsewhere:
 *  Scheduled (a schedule link, Search) or All done (Search's Deliverables). */
export type TasksFocus = { section: "scheduled" | "up-next" | "all" | "done" | "needs"; nonce: number; /** Opens that brief's detail beside Scheduled (Today's missed-brief Why). */ brief?: BriefSlot };

export type TasksPageProps = {
    /** Incremented by the app header Back button while a thread is open. */
    backSignal?: number;
    /** Lets the app header mirror whether a thread is open. */
    onReaderOpenChange?: (open: boolean) => void;
    /** Sends an accepted task's prompt through the normal chat execution path. */
    onStartHandoff: (result: HandoffAcceptResult, options?: HandoffStartOptions) => Promise<boolean>;
    /** Opens a playbook (Up next offer › Edit). */
    onOpenPlaybook?: (id: string) => void;
    wideMode?: boolean;
    openHandoffId?: string | null;
    onOpenChatMessage?: (sessionId: string, itemId: string) => void;
    /** Starts a task where it is, without leaving Tasks: it appears in Running. */
    onStartInPlace?: (result: HandoffAcceptResult) => Promise<boolean>;
    /** Tasks › Scheduled: GetUpcoming, read by the app while Tasks is open. */
    upcoming?: { items: UpcomingItem[]; loading: boolean; failed?: boolean; refresh?: () => void };
    /** Opens a schedule's detail beside the list. */
    onSelectSchedule?: (jobId: string) => void;
    /** Deletes a schedule after its confirm (Scheduled's hover X); resolves
     *  false when the delete didn't go through. */
    onDeleteSchedule?: (jobId: string) => Promise<boolean> | void;
    /** Opens Today on a brief (the brief's detail › Open on Today). */
    onOpenTodayBrief?: (briefId?: string) => void;
    /** Opens Settings where the brief's times are set. */
    onChangeBriefTimes?: () => void;
    focus?: TasksFocus | null;
};

export function startOfTaskWeek(now: Date): number {
    const monday = new Date(now);
    monday.setHours(0, 0, 0, 0);
    monday.setDate(monday.getDate() - (monday.getDay() + 6) % 7);
    return monday.getTime();
}

/** A task's title: the first line of what was typed, at most 200 bytes. */
export function taskTitleFrom(text: string): string {
    const line = text.trim().split("\n")[0].trim();
    const encoder = new TextEncoder();
    if (encoder.encode(line).length <= 200) return line;
    let out = "";
    for (const char of line) {
        if (encoder.encode(out + char + "…").length > 200) break;
        out += char;
    }
    return out + "…";
}

/** A schedule whose last two runs failed or did not happen. */
export function scheduleKeptFailing(item: UpcomingItem): boolean {
    return item.failedAttempts >= 2 || item.lastReason === "kept_failing" || item.missedRuns >= 2;
}

/** A schedule that will never run again (a one-off that already ran). */
export function scheduleFinished(item: UpcomingItem): boolean {
    return !item.enabled && item.scheduleKind === "at" && item.lastRunAt > 0 && item.nextRunAt <= 0;
}

/**
 * Tasks: what Isoform is doing for you (sidebar handoff 1.0.0, section 4). A
 * box to give it a task, then Needs you, Up next, Running, Scheduled and Done
 * this week. Empty sections hide, except Up next (Find more lives there) and
 * Scheduled (the briefs are there from setup on), so the page is never blank.
 */
export function TasksPage({ openHandoffId, onOpenChatMessage, onReaderOpenChange, onStartInPlace, upcoming, onSelectSchedule, onOpenTodayBrief, onChangeBriefTimes, onDeleteSchedule, focus, onOpenPlaybook, ...readerProps }: TasksPageProps) {
    const { t, i18n } = useTranslation("app");
    // Cache first (snappy-and-first-run-1.0.md, section 2): the groups paint
    // from the last read, warmed after launch, and update in place.
    const [handoffs, setHandoffs] = useState<HandoffEntry[]>(() => pageQueryCache.peek<HandoffEntry[]>("ListHandoffs") ?? []);
    const [taskViews, setTaskViews] = useState<DesktopTaskThread[]>([]);
    const [listScheduled, setListScheduled] = useState<DesktopScheduledTask[]>([]);
    const [loading, setLoading] = useState(() => pageQueryCache.peek("ListHandoffs") === undefined);
    const [failed, setFailed] = useState(false);
    // When the list on screen was last read, for "showing {time}" (T30).
    const [loadedAt, setLoadedAt] = useState<number | null>(() => pageQueryCache.peek("ListHandoffs") === undefined ? null : Date.now());
    const [deliverablesFailed, setDeliverablesFailed] = useState(false);
    const [stopFailed, setStopFailed] = useState<string | null>(null);
    const [surface, setSurface] = useState<"board" | "all">("board");
    // A link to a task that was deleted (K2).
    const [gone, setGone] = useState(false);
    const [startFailed, setStartFailed] = useState<string | null>(null);
    const [notSaved, setNotSaved] = useState<string | null>(null);
    const [showPutOff, setShowPutOff] = useState(false);
    const [selectedId, setSelectedId] = useState(openHandoffId);
    const [briefSlot, setBriefSlot] = useState<BriefSlot | null>(null);
    const waiting = useTodayNeeds();
    const waitingPanel = useMainChatItemPanel("tasks");
    const [deliverables, setDeliverables] = useState<DeliverableEntry[]>([]);
    const [firstWeekDay, setFirstWeekDay] = useState<number | null>(null);
    const { answers } = useOpenAnswers();
    const pendingStarts = usePendingTaskStarts();
    const pendingRestarts = usePendingRestarts();
    const generation = useRef(0);
    const taskViewGeneration = useRef(0);
    const lastOpenId = useRef(openHandoffId);
    const lastBackSignal = useRef(readerProps.backSignal);
    const scheduledRef = useRef<HTMLElement>(null);
    const upNextRef = useRef<HTMLDivElement>(null);
    const doneRef = useRef<HTMLElement>(null);
    const needsRef = useRef<HTMLElement>(null);
    const playbookCards = usePlaybookOfferCards(onOpenPlaybook);
    const reload = useCallback(async () => {
        const request = ++generation.current;
        try {
            const rows = await ListHandoffs();
            if (request === generation.current) {
                setHandoffs(rows ?? []);
                setFailed(false);
                setLoadedAt(Date.now());
            }
        } catch {
            if (request === generation.current) setFailed(true);
        } finally {
            if (request === generation.current) setLoading(false);
        }
    }, []);
    const reloadDrafts = useCallback(() => {
        const request = ++taskViewGeneration.current;
        void Promise.resolve().then(() => ListDesktopTaskThreads()).then((page) => { if (request === taskViewGeneration.current) { setTaskViews(page.tasks ?? []); setListScheduled((page.scheduled ?? []).filter((item) => item.kind === "reminder")); } }).catch(() => { if (request === taskViewGeneration.current) setFailed(true); });
    }, []);
    const reloadDeliverables = useCallback(() => {
        return Promise.resolve().then(() => ListDeliverables()).then((rows) => { setDeliverables(Array.isArray(rows) ? rows : []); setDeliverablesFailed(false); return true; }).catch(() => { setDeliverablesFailed(true); return false; });
    }, []);
    // After an action or a change event the next read must not be the cached
    // one; opening the page may be.
    const reloadFresh = useCallback(() => {
        pageQueryCache.invalidate(["ListHandoffs"], false);
        void reloadDeliverables();
        reloadDrafts();
        return reload();
    }, [reload, reloadDeliverables, reloadDrafts]);
    usePageQueryRefresh(["ListHandoffs"], reload);
    useEffect(() => {
        void reload();
        void reloadDeliverables();
        reloadDrafts();
        GetFirstWeekDay().then((day) => setFirstWeekDay(typeof day === "number" ? day : -1)).catch(() => setFirstWeekDay(-1));
        const unsubscribe = EventsOn("handoff:updated", () => void reloadFresh());
        const withdraw = onPagePrivacyWithdrawal(() => { taskViewGeneration.current++; setTaskViews([]); setListScheduled([]); reloadDrafts(); });
        return () => { generation.current++; taskViewGeneration.current++; unsubscribe?.(); withdraw(); };
    }, [reload]);
    useEffect(() => {
        if (!loading) markPageContent("tasks");
    }, [loading]);
    useEffect(() => {
        if (openHandoffId && openHandoffId !== lastOpenId.current) {
            setSelectedId(openHandoffId);
            setSurface("board");
            setGone(false);
        }
        lastOpenId.current = openHandoffId;
    }, [openHandoffId]);
    useEffect(() => {
        onReaderOpenChange?.(!!selectedId);
    }, [selectedId, onReaderOpenChange]);
    useEffect(() => {
        if (readerProps.backSignal !== lastBackSignal.current && selectedId) {
            setSelectedId(null);
            void reloadFresh();
        }
        lastBackSignal.current = readerProps.backSignal;
    }, [readerProps.backSignal, selectedId, reloadFresh]);
    useEffect(() => {
        if (!focus) return;
        if (focus.section === "all") {
            setSelectedId(null);
            setBriefSlot(null);
            setSurface("all");
            return;
        }
        setSurface("board");
        if (focus.brief) {
            setSelectedId(null);
            setBriefSlot(focus.brief);
        }
        requestAnimationFrame(() => (focus.section === "up-next" ? upNextRef.current : focus.section === "done" ? doneRef.current : focus.section === "needs" ? needsRef.current : scheduledRef.current)?.scrollIntoView?.({ block: "start", behavior: "smooth" }));
    }, [focus?.nonce, loading, focus?.section === "done" ? handoffs : null, focus?.section === "done" ? deliverables : null, focus?.section === "needs" ? waiting.items : null]);
    const open = (id: string, action?: TasksActionId) => {
        if (action) recordTasksAction(action, "open", "opened");
        waitingPanel.close();
        setBriefSlot(null);
        setSelectedId(id);
        setGone(false);
    };
    const closeThread = () => { setSelectedId(null); void reloadFresh(); };
    // Do it waits 5 s with "Started · Undo" (T05); a failed start puts the
    // row back with "Couldn't start it. Try again".
    const runStart = async (id: string) => {
        const accepted = await AcceptHandoff(id);
        if (!await readerProps.onStartHandoff(accepted)) throw new Error("chat did not start");
    };
    const start = (id: string) => {
        setStartFailed(null);
        const started = performance.now();
        beginRestart(id, () => { setStartFailed(id); recordTasksAction("T05", "do", "failed", started); }, () => { recordTasksAction("T05", "do", "done", started); void reloadFresh(); }, () => runStart(id));
    };
    const putOff = (item: TodayNeed, action: "put_off" | "bring_back" | "keep" | "forget") => {
        setNotSaved(null);
        void measureTasks("T06", "do", action === "bring_back" ? "undone" : "done", () => putOffTodayNeed(item, action)).catch(() => { if (action === "put_off" || action === "forget") setNotSaved(item.id); });
    };
    const markDone = (item: TodayNeed) => {
        setNotSaved(null);
        if (item.targetKind !== "handoff") { putOff(item, "forget"); return; }
        void ActOnDesktopTask(item.targetId, { action: "mark_done" }).then(() => void reloadFresh()).catch(() => setNotSaved(item.id));
    };
    // A failed Stop says so on the row (T16), never as a toast.
    const stop = async (h: HandoffEntry) => {
        setStopFailed(null);
        try {
            await measureTasks("T16", "do", "stopped", () => AbortHandoff(h.id));
            setHandoffs((current) => current.map((row) => row.id === h.id ? { ...row, status: "aborted", updatedAt: Date.now() } : row));
        } catch {
            setStopFailed(h.id);
        } finally {
            void reloadFresh();
        }
    };
    const weekStart = startOfTaskWeek(new Date());
    const drafting = taskViews.filter((task) => task.status === "drafting" && task.group === "running");
    const hiddenFromNeeds = new Set([...drafting.map((task) => task.id), ...listScheduled.map((task) => task.id)]);
    const needs = waiting.items.filter((item) => item.targetKind !== "handoff" || !hiddenFromNeeds.has(item.targetId));
    const listNeeds = taskViews.filter((task) => task.source === "list" && task.group === "needs_you" && !needs.some((item) => item.targetKind === "handoff" && item.targetId === task.id));
    const running = handoffs.filter((h) => h.status === "in_progress");
    const openAnswers = answers.filter((answer) => answer.state === "open");
    const handoffFiles = new Map(deliverables.filter((d) => d.source === "handoff").map((d) => [d.sourceId, d] as const));
    const stoppedByYou = (h: HandoffEntry) => h.status === "aborted" && h.stoppedBy === "person";
    // Stopped before stops were recorded: who stopped it is unknown.
    const stoppedUnrecorded = (h: HandoffEntry) => h.status === "aborted" && !h.stoppedBy;
    const stopTime = (h: HandoffEntry) => computerClock(new Date(h.stoppedAt || h.updatedAt), i18n.language);
    const handoffOutcome = (h: HandoffEntry) => taskOutcomeLine(taskViews.find((task) => task.id === h.id)?.outcome, i18n.language, t)
        ?? (stoppedByYou(h) ? t("taskThread.youStopped", { time: stopTime(h) }) : stoppedUnrecorded(h) ? t("taskThread.stoppedAt", { time: stopTime(h) }) : undefined);
    const allDoneHandoffs = handoffs.filter((h) => h.status === "completed" || stoppedByYou(h) || stoppedUnrecorded(h));
    const doneHandoffs = allDoneHandoffs.filter((h) => h.updatedAt >= weekStart);
    // Work finished in Chat that made a file counts as done.
    const doneChats = deliverables.filter((d) => d.source === "chat" && d.status === "completed" && (d.files?.length ?? 0) > 0 && d.updatedAt >= weekStart && d.summary?.trim());
    // A scheduled run that finished with its files is finished work too (tasks-01's Weekly update).
    const finishedRuns = deliverables.filter((d) => d.source === "automation" && d.status === "completed");
    // Finished drafts: every other finished task is a handoff row.
    const finishedTasks = taskViews.filter((task) => task.id.startsWith("draft.") && task.group === "done");
    const runRow = (d: DeliverableEntry): DoneRow => ({ key: `automation:${d.id}`, title: d.title, at: d.updatedAt, outcome: d.summary || undefined, entry: d, open: undefined });
    const doneRow = (h: HandoffEntry): DoneRow => ({ key: `handoff:${h.id}`, title: h.title, at: h.updatedAt, outcome: handoffOutcome(h), entry: handoffFiles.get(h.id) ?? null, open: (action: TasksActionId) => open(h.id, action), byYou: stoppedByYou(h) || stoppedUnrecorded(h) });
    // All done lists every finished task, with or without files (K2).
    const allDone: DoneRow[] = [
        ...allDoneHandoffs.map(doneRow),
        ...finishedTasks.map((task) => ({ key: task.id, title: task.title, at: Date.parse(task.updatedAt), outcome: taskOutcomeLine(task.outcome, i18n.language, t), entry: null, open: (action: TasksActionId) => open(task.id, action) })),
        ...deliverables.filter((d) => d.source === "chat" && d.status === "completed" && (d.files?.length ?? 0) > 0).map((d) => ({ key: `chat:${d.id}`, title: d.title, at: d.updatedAt, outcome: d.summary?.trim() || undefined, entry: d, open: undefined })),
        ...finishedRuns.map(runRow),
    ].sort((a, b) => b.at - a.at);
    const threadAside = selectedId && <aside className="flex w-[420px] min-h-0 shrink-0 flex-col border-l border-border" data-testid="tasks-thread-panel"><div className="flex justify-end px-2 pt-2"><Button variant="quiet" size="icon" aria-label={t("tasks.back")} onClick={() => { recordTasksAction("H05", "open", "opened"); closeThread(); }}><X className="size-4" /></Button></div><TaskThreadPane id={selectedId} tasksMeasured onClose={() => { recordTasksAction("H05", "open", "opened"); closeThread(); }} onDoIt={() => runStart(selectedId)} onGone={() => { recordTasksAction("H06", "open", "failed"); setSelectedId(null); setGone(true); }} onDeleted={() => { setHandoffs((rows) => rows.filter((row) => row.id !== selectedId)); closeThread(); }} /></aside>;
    const done: DoneRow[] = [
        ...doneHandoffs.map(doneRow),
        ...finishedTasks.filter((task) => Date.parse(task.updatedAt) >= weekStart && taskOutcomeLine(task.outcome, i18n.language, t)).map((task) => ({ key: task.id, title: task.title, at: Date.parse(task.updatedAt), outcome: taskOutcomeLine(task.outcome, i18n.language, t), entry: null, open: (action: TasksActionId) => open(task.id, action) })),
        ...doneChats.map((d) => ({ key: `chat:${d.id}`, title: d.title, at: d.updatedAt, outcome: d.summary, entry: d, open: undefined })),
        ...finishedRuns.filter((d) => d.updatedAt >= weekStart).map(runRow),
    ].sort((a, b) => b.at - a.at);
    // All done can't say "nothing yet" when a read behind it failed (D03).
    const allDoneFailed = deliverablesFailed || (failed && loadedAt === null);
    const showAll = () => { recordTasksAction("T28", "open", "opened"); setSelectedId(null); setSurface("all"); };
    const runningRows = running.length + drafting.length + openAnswers.length + pendingStarts.length;
    const needsRows = needs.length + listNeeds.length;
    useTasksSectionSeen("needs_you", needsRows, surface === "board" && !waiting.loading && needsRows > 0);
    useTasksSectionSeen("running", runningRows, surface === "board" && runningRows > 0);
    useTasksSectionSeen("done_this_week", done.length, surface === "board" && !loading && done.length > 0);
    useTasksSectionSeen("all_done", allDone.length, surface === "all" && !loading);
    if (surface === "all") return (
        <div className="flex flex-1 min-h-0">
            <div className="flex-1 min-w-0 min-h-0 overflow-y-auto">
                <div className="mx-auto flex max-w-[680px] flex-col gap-4 px-8 pt-6 pb-12">
                    <div><Button variant="quiet" size="sm" onClick={() => { recordTasksAction("T28", "open", "opened"); setSurface("board"); void reloadFresh(); }}><ArrowLeft className="size-4" />{t("tasks.back")}</Button></div>
                    <section aria-label={t("tasks.allDone")} data-tasks-section="all-done" data-testid="all-done">
                        <TaskSectionHeader title={t("tasks.allDone")} />
                        {allDoneFailed && <FailureLine testId="all-done-failed" text={t("tasks.failure.allDone")} onRetry={() => void measureTasks("D03", "do", "done", () => reloadFresh())} />}
                        {allDone.length > 0 && <TaskList>{allDone.map((row) => <DoneTaskRow key={row.key} row={row} language={i18n.language} surface="all" />)}</TaskList>}
                    </section>
                </div>
            </div>
            {selectedId && threadAside}
        </div>
    );
    const firstDay = firstWeekDay === 0;
    const runningCount = runningRows;
    const needsCount = needsRows;
    const summary = needsCount === 0 && runningCount === 0
        ? firstDay ? t("tasks.lineFirstDay") : t("tasks.lineNothing")
        : [
            needsCount > 0 && t("tasks.summaryNeeds", { count: needsCount }),
            runningCount > 0 && t("tasks.summaryRunning", { count: runningCount }),
        ].filter(Boolean).join(" · ");
    const subtitle = (h: HandoffEntry) => {
        const stopped = stopRowLine(h.stopCause, h.stopAssistant, t);
        if (stopped) return stopped;
        if (h.status === "needs_review" && h.verification?.total) return t("taskThread.checksMet", { met: h.verification.met, total: h.verification.total });
        const reason = handoffBlockedReasonKey(h);
        if (reason) return t(reason);
        if (h.status === "blocked") return h.blockedReason || t("tasks.blocked");
        return h.status === "active" ? t("tasks.awaitingStart") : t("tasks.ready");
    };
    return (
        <div className="flex flex-1 min-h-0">
        <div className="flex-1 min-w-0 min-h-0 overflow-y-auto">
            <div className="mx-auto flex max-w-[680px] flex-col gap-7 px-8 pt-9 pb-12">
                <header>
                    <p className="text-[12px] font-medium text-accent">{t("sidebar.tasks")}</p>
                    <h1 className="mt-1 text-[24px] font-semibold tracking-[-0.01em] text-ink">{t("tasks.heading")}</h1>
                    <p data-testid="tasks-line" className="mt-1.5 text-[14px] text-muted">{summary}</p>
                    <TaskComposer onStartInPlace={onStartInPlace} onCreated={reloadFresh} />
                </header>
                {gone && <p data-testid="tasks-gone" className="text-[13px] text-muted">{t("tasks.gone")}</p>}
                {failed && <FailureLine testId="tasks-refresh-failed" text={loadedAt === null ? t("tasks.failure.refreshNoTime") : t("tasks.failure.refresh", { time: computerClock(new Date(loadedAt), i18n.language) })} joiner=" · " onRetry={() => void measureTasks("T30", "do", "done", () => reloadFresh())} />}
                {(needsCount > 0 || waiting.loading || waiting.putOff.length > 0) && (
                    <section ref={needsRef} aria-label={t("tasks.needsYou")} aria-busy={waiting.loading || undefined} data-tasks-section="needs-you">
                        <TaskSectionHeader title={t("tasks.needsYou")} />
                        {waiting.loading ? <TaskRowPlaceholders /> : (
                            <div className="flex flex-col gap-2.5">
                                {needs.map((item) => {
                                    const handoff = item.targetKind === "handoff" ? handoffs.find((row) => row.id === item.targetId) : undefined;
                                    const listTask = taskViews.find((task) => task.id === item.targetId && task.source === "list");
                                    const openItem = () => {
                                        if (handoff) open(handoff.id, "T04");
                                        else if (item.targetKind === "chat_item") {
                                            recordTasksAction("T04", "open", "opened");
                                            setSelectedId(null);
                                            setSurface("board");
                                            waitingPanel.openMoment(item.id);
                                        }
                                    };
                                    return <div key={item.id} data-needs-you-card={item.id} className="flex items-center gap-3 rounded-xl border border-border bg-panel px-4 py-3.5">
                                        <span aria-hidden="true" className="size-2 shrink-0 rounded-full bg-copper" />
                                        <button className="min-w-0 flex-1 text-left" onClick={openItem}>
                                            <span className="block text-[14px] font-semibold text-ink">{todayReviewSentence(item, i18n.resolvedLanguage)}</span>
                                            {notSaved === item.id && <span role="status" className="mt-1 block text-[13px] text-muted">{t("tasks.notNowNotSaved")}</span>}
                                            {(handoff || item.source) && <span className={cn("mt-1 block text-[13px]", handoff?.stopCause === "loop_guard" ? "text-copper" : "text-muted")}>{handoff ? subtitle(handoff) : item.source}</span>}
                                        </button>
                                        {item.asksKeep ? <div data-testid="needs-asks-keep" className="flex shrink-0 gap-2">
                                            <Button variant="secondary" size="sm" onClick={() => putOff(item, "keep")}>{t("tasks.keep")}</Button>
                                            <Button variant="secondary" size="sm" onClick={() => markDone(item)}>{t("tasks.done")}</Button>
                                            <Button variant="secondary" size="sm" onClick={() => putOff(item, "forget")}>{t("tasks.forget")}</Button>
                                        </div>
                                        : handoff && handoff.status === "active" && !listTask && pendingRestarts[handoff.id] ? <div className="flex shrink-0 items-center gap-2"><span className="text-[13px] text-muted">{t("taskThread.started")}</span>{!!pendingRestarts[handoff.id]?.timer && <Button variant="quiet" size="sm" onClick={() => undoRestart(handoff.id)}>{t("taskThread.undo")}</Button>}</div>
                                        : handoff && stopRowButton(handoff.stopCause) ? <StopRowAction id={handoff.id} button={stopRowButton(handoff.stopCause)!} pending={!!pendingRestarts[handoff.id]} undoable={!!pendingRestarts[handoff.id]?.timer} onDone={reloadFresh} />
                                        : handoff?.status === "active" && !listTask ? <div className="flex shrink-0 gap-2">
                                            {startFailed === handoff.id && <FailureLine tone="copper" className="self-center" text={t("taskThread.startFailed")} />}
                                            <Button variant="secondary" size="sm" onClick={() => putOff(item, "put_off")}>{t("tasks.notNow")}</Button>
                                            <Button size="sm" onClick={() => start(handoff.id)}>{startFailed === handoff.id ? t("taskThread.retry") : t("tasks.doIt")}</Button>
                                        </div> : <Button size="sm" className="shrink-0" onClick={openItem}>{t("tasks.review")}</Button>}
                                    </div>;
                                })}
                                {waiting.putOff.length > 0 && <div data-testid="needs-put-off" className="px-1 text-[13px] text-muted">
                                    <p className="flex items-center gap-1">{t("tasks.putOff", { count: waiting.putOff.length })} ·<Button variant="quiet" size="sm" aria-expanded={showPutOff} onClick={() => setShowPutOff(!showPutOff)}>{showPutOff ? t("tasks.putOffHide") : t("tasks.putOffShow")}</Button></p>
                                    {showPutOff && <ul className="mt-1 flex flex-col gap-1">{waiting.putOff.map((item) => <li key={item.id} data-put-off={item.id} className="flex items-center gap-2"><span className="min-w-0 flex-1 truncate text-ink">{todayReviewSentence(item, i18n.resolvedLanguage)}</span><Button variant="quiet" size="sm" onClick={() => putOff(item, "bring_back")}>{t("tasks.bringBack")}</Button></li>)}</ul>}
                                </div>}
                                {listNeeds.map((task) => <div key={task.id} className="flex items-center gap-3 rounded-xl border border-border bg-panel px-4 py-3.5"><span aria-hidden="true" className="size-2 shrink-0 rounded-full bg-copper" /><button className="min-w-0 flex-1 text-left text-[14px] font-semibold text-ink" onClick={() => open(task.id, "T04")}>{task.title}</button><Button size="sm" onClick={() => open(task.id, "T04")}>{t("tasks.review")}</Button></div>)}
                            </div>
                        )}
                    </section>
                )}
                <div ref={upNextRef}><TaskSuggestionDeck firstDay={firstDay} extraCards={playbookCards} /></div>
                {runningCount > 0 && (
                    <section aria-label={t("tasks.running")} data-tasks-section="running">
                        <TaskSectionHeader title={t("tasks.running")} />
                        <TaskList>
                            {pendingStarts.map(({ offer, timer }) => <div key={offer.id} className="flex items-center gap-3 px-4 py-3"><div className="min-w-0 flex-1"><span className="block truncate text-sm font-medium text-ink">{offer.title}</span><span className="text-xs text-muted">{t("taskThread.started")}</span></div>{timer !== 0 && <Button variant="quiet" size="sm" onClick={() => undoTaskStart(offer.id)}>{t("taskThread.undo")}</Button>}</div>)}
                            {drafting.map((task) => <button key={task.id} className="flex w-full items-center gap-3 px-4 py-3 text-left" onClick={() => open(task.id, "T15")}><TaskProgress /><span className="min-w-0"><span className="block truncate text-[14px] font-medium text-ink">{task.title}</span><span className="mt-0.5 block text-[12px] text-muted">{t("tasks.brainDumpDrafting")}</span></span></button>)}
                            {running.map((h) => (
                                <div key={h.id} className="px-4 py-3">
                                    <div className="flex items-center gap-3">
                                    <button className="flex min-w-0 flex-1 items-center gap-3 text-left" onClick={() => open(h.id, "T15")}>
                                        <TaskProgress progress={h.progress} />
                                        <span className="min-w-0 flex-1">
                                            <span className="block truncate text-[14px] font-medium text-ink">{h.title}</span>
                                            <span className="mt-0.5 block truncate text-[12px] text-muted">{runningLine(h, t)}</span>
                                        </span>
                                    </button>
                                    <Button variant="quiet" size="sm" className="shrink-0 text-muted" onClick={() => void stop(h)}>{t("tasks.stop")}</Button>
                                    </div>
                                    {stopFailed === h.id && <FailureLine tone="copper" className="mt-1 pl-10" testId="task-stop-failed" text={t("tasks.failure.stop")} onRetry={() => void stop(h)} />}
                                </div>
                            ))}
                            {openAnswers.map((answer) => <OpenAnswerTaskRow key={answer.id} answer={answer} onOpen={() => { recordTasksAction("T17", "open", "opened"); onOpenChatMessage?.(answer.sessionId, answer.messageId); }} />)}
                        </TaskList>
                    </section>
                )}
                <ScheduledSection sectionRef={scheduledRef} upcoming={upcoming} listScheduled={listScheduled} onOpenListTask={(id) => open(id, "T22")} onSelectSchedule={onSelectSchedule} onOpenBrief={(slot) => { waitingPanel.close(); setSelectedId(null); setBriefSlot(slot); }} openBrief={selectedId ? null : briefSlot} onDeleteSchedule={onDeleteSchedule} />
                {(done.length > 0 || deliverablesFailed) && (
                    <section ref={doneRef} aria-label={t("tasks.doneThisWeek")} data-tasks-section="done">
                        <TaskSectionHeader title={t("tasks.doneThisWeek")}
                            action={done.length > 0 ? <Button variant="quiet" size="sm" className="text-muted" onClick={showAll}>{t("tasks.showAll")}</Button> : undefined} />
                        {deliverablesFailed && <FailureLine testId="done-week-failed" className={done.length > 0 ? "mb-2" : undefined} text={t("tasks.failure.doneThisWeek")} onRetry={() => void measureTasks("T29", "do", "done", () => reloadDeliverables())} />}
                        {done.length > 0 && <TaskList>
                            {done.slice(0, 5).map((row) => <DoneTaskRow key={row.key} row={row} language={i18n.language} surface="board" />)}
                        </TaskList>}
                    </section>
                )}
                {(allDone.length > 0 || deliverablesFailed) && <div className="flex justify-center"><Button variant="quiet" size="sm" data-testid="all-done-link" className="text-muted" onClick={showAll}>{t("tasks.allDoneLink")}</Button></div>}
            </div>
        </div>
        <MainChatItemPanel surface="tasks" />
        {threadAside}
        {!selectedId && briefSlot && <BriefDetailPane slot={briefSlot} onClose={() => setBriefSlot(null)} onOpenToday={onOpenTodayBrief} onChangeTimes={onChangeBriefTimes} />}
        </div>
    );
}

type DoneRow = { key: string; title: string; at: number; outcome?: string; entry: DeliverableEntry | null; open?: (action: TasksActionId) => void; byYou?: boolean };

/** A stopped task's one button: Set up and Sign in open Your AI; Start again
 *  waits 5 s with "Started · Undo". */
function StopRowAction({ id, button, pending, undoable, onDone }: { id: string; button: "set_up" | "sign_in" | "start_again"; pending: boolean; undoable: boolean; onDone: () => void }) {
    const { t } = useTranslation("app");
    const [failed, setFailed] = useState(false);
    if (pending) return <div className="flex shrink-0 items-center gap-2"><span className="text-[13px] text-muted">{t("taskThread.started")}</span>{undoable && <Button variant="quiet" size="sm" onClick={() => undoRestart(id)}>{t("taskThread.undo")}</Button>}</div>;
    switch (button) {
        case "set_up": return <Button size="sm" className="shrink-0" onClick={openYourAI}>{t("taskThread.setUp")}</Button>;
        case "sign_in": return <Button size="sm" className="shrink-0" onClick={openYourAI}>{t("taskThread.signIn")}</Button>;
        case "start_again": return <div className="flex shrink-0 items-center gap-2">{failed && <FailureLine tone="copper" text={t("taskThread.startFailed")} />}<Button size="sm" onClick={() => { setFailed(false); beginRestart(id, () => setFailed(true), onDone); }}>{t("taskThread.startAgain")}</Button></div>;
    }
}

function runningLine(h: HandoffEntry, t: ReturnType<typeof useTranslation<"app">>["t"]): string {
    const who = h.runner === "claude_code" ? t("tasks.claudeCode") : h.runner === "codex" ? t("tasks.codex") : "";
    const what = h.progress ? t("tasks.progress", { count: h.progress.total, done: h.progress.done, total: h.progress.total, label: h.progress.label }) : t("tasks.working");
    return who ? t("tasks.runningLine", { who, what }) : what;
}

/** The rows of a section in one rounded box, divided by hairlines. */
function TaskList({ children }: { children: ReactNode }) {
    return <div className="divide-y divide-border-divider overflow-hidden rounded-xl border border-border bg-panel">{children}</div>;
}

/** Parse first; only the checked rows start when the person adds them. */
function TaskComposer({ onCreated }: { onStartInPlace?: (result: HandoffAcceptResult) => Promise<boolean>; onCreated: () => Promise<void> }) {
    const { t, i18n } = useTranslation("app");
    const [text, setText] = useState("");
    const [items, setItems] = useState<BrainDumpItem[] | null>(null);
    const [checked, setChecked] = useState<Set<string>>(new Set());
    const [addAttempted, setAddAttempted] = useState(false);
    const [pending, setPending] = useState(false);
    const [failure, setFailure] = useState<"make" | "add" | null>(null);
    // No resize grip: the box grows with its text (tasks-review v1.0.3 #6).
    const boxRef = useRef<HTMLTextAreaElement>(null);
    useLayoutEffect(() => {
        const box = boxRef.current;
        if (!box) return;
        box.style.height = "auto";
        if (box.scrollHeight > 0) box.style.height = `${box.scrollHeight}px`;
    }, [text, items]);
    const addKey = useRef("");
    const lock = useRef(false);
    const make = async () => {
        if (!text.trim() || lock.current) return;
        lock.current = true;
        setPending(true);
        setFailure(null);
        try {
            const result = await measureTasks("T01", "do", "done", () => ParseBrainDump(text));
            setItems(result.items);
            setChecked(new Set(result.items.map((item) => item.id)));
            addKey.current = typeof crypto !== "undefined" && typeof crypto.randomUUID === "function" ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
            setAddAttempted(false);
        } catch {
            setFailure("make");
        } finally {
            lock.current = false;
            setPending(false);
        }
    };
    const add = async () => {
        if (!items || checked.size === 0 || lock.current) return;
        lock.current = true;
        setPending(true);
        setFailure(null);
        setAddAttempted(true);
        try {
            await measureTasks("T01", "do", "done", () => AddBrainDump(addKey.current, items.filter((item) => checked.has(item.id))));
            setText("");
            setItems(null);
            setAddAttempted(false);
            await onCreated();
        } catch {
            setFailure("add");
        } finally {
            lock.current = false;
            setPending(false);
        }
    };
    const who = (item: BrainDumpItem) => {
        switch (item.who) {
        case "draft": return t("tasks.brainDumpDraft");
        case "claude_code": return t("tasks.brainDumpClaude");
        case "codex": return t("tasks.brainDumpCodex");
        case "reminder":
            if (item.noRunner) return t("tasks.brainDumpNoRunner");
            if (!item.when || Number.isNaN(Date.parse(item.when))) return t("tasks.brainDumpReminder");
            return t("tasks.brainDumpReminderAt", { when: new Intl.DateTimeFormat(i18n.language, { dateStyle: "medium", timeStyle: "short" }).format(new Date(item.when)) });
        }
    };
    const source = (item: BrainDumpItem) => {
        switch (item.who) {
        case "draft": return item.person?.trim() ? t("tasks.brainDumpReplySource", { name: item.person.trim() }) : undefined;
        case "claude_code": return item.project?.trim() ? t("tasks.brainDumpClaudeProject", { project: item.project.trim() }) : undefined;
        case "codex": return item.project?.trim() ? t("tasks.brainDumpCodexProject", { project: item.project.trim() }) : undefined;
        case "reminder": return item.person?.trim() ? t("tasks.brainDumpReplyNoEmail") : undefined;
        }
    };
    return (
        <div className="mt-5 rounded-xl border border-border-input bg-panel p-4 focus-within:border-border-focus">
            {items ? <>
                <p className="mb-3 text-[13px] text-muted">{t("tasks.brainDumpCheck")}</p>
                <div className="divide-y divide-border-divider rounded-lg border border-border">
                    {items.map((item) => <label key={item.id} className="flex cursor-pointer items-start gap-3 px-3 py-3">
                        <input type="checkbox" checked={checked.has(item.id)} disabled={pending || addAttempted} onChange={() => setChecked((old) => {
                            const next = new Set(old); if (next.has(item.id)) next.delete(item.id); else next.add(item.id); return next;
                        })} className="mt-1 accent-accent" />
                        <span className="min-w-0"><span className="block text-sm font-medium text-ink">{item.title}</span><span className="mt-0.5 block text-xs text-muted">{who(item)}</span>{source(item) && <span className="mt-0.5 block text-xs text-muted">{source(item)}</span>}</span>
                    </label>)}
                </div>
                {failure === "add" && <FailureLine tone="copper" className="mt-3" text={t("tasks.brainDumpAddFailed")} />}
                <div className="mt-3 flex items-center justify-between gap-3">
                    <Button type="button" variant="quiet" size="sm" disabled={pending || addAttempted} onClick={() => { setItems(null); setFailure(null); }}>{t("tasks.brainDumpEdit")}</Button>
                    <Button type="button" size="sm" disabled={pending || checked.size === 0} onClick={() => void add()}>{t("tasks.brainDumpAdd", { count: checked.size })}</Button>
                </div>
            </> : <form onSubmit={(event) => { event.preventDefault(); void make(); }}>
                <textarea ref={boxRef} value={text} disabled={pending} onChange={(event) => setText(event.target.value)} placeholder={t("tasks.composerPlaceholder")} aria-label={t("tasks.composerPlaceholder")}
                    rows={3} data-testid="task-box" className="w-full resize-none overflow-hidden bg-transparent text-[14px] text-ink outline-none placeholder:text-subtle" />
                {failure === "make" && <FailureLine tone="copper" className="mt-2" text={t("tasks.brainDumpMakeFailed")} />}
                <div className="mt-2 flex justify-end"><Button type="submit" size="sm" disabled={pending || !text.trim()}>{pending ? t("tasks.brainDumpMaking") : t("tasks.brainDumpMake")}</Button></div>
            </form>}
        </div>
    );
}

/** A playbook Isoform drafted from what it saw, offered in Up next: Do it
 *  confirms and schedules it, Not now puts the offer away. */
function usePlaybookOfferCards(onOpenPlaybook?: (id: string) => void): ReactNode[] {
    const { t, i18n } = useTranslation("app");
    const library = usePlaybookLibrary();
    const [answering, setAnswering] = useState<string | null>(null);
    // Not now hides the offer at once, with Undo; after the Undo window it is
    // snoozed for two weeks, never declined (sidebar handoff, Up next).
    const [hidden, setHidden] = useState<ReadonlySet<string>>(new Set());
    // Not now and Do it wait in place for 5 s with Undo, never as a toast
    // (T13, T14); a failed save puts the card back with a line (T09).
    const [away, setAway] = useState<ReadonlySet<string>>(new Set());
    const [added, setAdded] = useState<ReadonlySet<string>>(new Set());
    const [failedLine, setFailedLine] = useState<{ id: string; line: string } | null>(null);
    const snoozeTimers = useRef(new Map<string, number>());
    const addTimers = useRef(new Map<string, number>());
    useEffect(() => () => {
        for (const [id, timer] of addTimers.current) {
            window.clearTimeout(timer);
            void AcceptPlaybookSuggestion(id).catch(() => {});
        }
        addTimers.current.clear();
    }, []);
    useEffect(() => () => {
        // Leaving Tasks inside the Undo window still snoozes.
        for (const [id, timer] of snoozeTimers.current) {
            window.clearTimeout(timer);
            void SnoozePlaybookSuggestion(id).catch(() => {});
        }
        snoozeTimers.current.clear();
    }, []);
    const offers = useMemo(() => (library.playbooks?.plans ?? [])
        .filter((plan) => offerShowsNow(plan) && (!hidden.has(plan.id) || away.has(plan.id)))
        .map((plan) => ({ plan, entry: library.sops.find((sop) => sop.id === plan.id) }))
        .filter((item): item is { plan: PlaybookView; entry: SopEntry } => Boolean(item.entry))
        .sort((a, b) => (b.plan.proposal?.proposedAt ?? 0) - (a.plan.proposal?.proposedAt ?? 0)), [library.playbooks, library.sops, hidden, away]);
    const without = (id: string) => (old: ReadonlySet<string>) => { const next = new Set(old); next.delete(id); return next; };
    const commitAccept = (entry: SopEntry) => {
        window.clearTimeout(addTimers.current.get(entry.id));
        addTimers.current.delete(entry.id);
        setAnswering(entry.id);
        return measureTasks("T13", "do", "done", () => AcceptPlaybookSuggestion(entry.id))
            .then(() => { library.reload(); })
            .catch((err) => {
                setAdded(without(entry.id));
                setFailedLine({ id: entry.id, line: isSitesChanged(err) ? t("sopsPage.delivery.sitesChanged") : t("sopsPage.suggestion.acceptFailed", { error: String(err) }) });
                throw err;
            })
            .finally(() => setAnswering(null));
    };
    const accept = (entry: SopEntry) => {
        if (answering || added.has(entry.id)) return;
        setFailedLine(null);
        setAdded((old) => new Set(old).add(entry.id));
        addTimers.current.set(entry.id, window.setTimeout(() => void commitAccept(entry).catch(() => {}), NOT_NOW_UNDO_MS));
    };
    const undoAccept = (id: string) => {
        recordTasksAction("T13", "do", "undone");
        window.clearTimeout(addTimers.current.get(id));
        addTimers.current.delete(id);
        setAdded(without(id));
    };
    const edit = (entry: SopEntry) => void commitAccept(entry).then(() => onOpenPlaybook?.(entry.id)).catch(() => {});
    const notNow = (entry: SopEntry) => {
        setFailedLine(null);
        setHidden((old) => new Set(old).add(entry.id));
        setAway((old) => new Set(old).add(entry.id));
        const timer = window.setTimeout(() => {
            snoozeTimers.current.delete(entry.id);
            setAway(without(entry.id));
            measureTasks("T14", "do", "done", () => SnoozePlaybookSuggestion(entry.id))
                .then(() => library.reload())
                .catch(() => { setHidden(without(entry.id)); setFailedLine({ id: entry.id, line: t("tasks.notNowNotSaved") }); });
        }, NOT_NOW_UNDO_MS);
        snoozeTimers.current.set(entry.id, timer);
    };
    const undoNotNow = (id: string) => {
        recordTasksAction("T14", "do", "undone");
        window.clearTimeout(snoozeTimers.current.get(id));
        snoozeTimers.current.delete(id);
        setHidden(without(id));
        setAway(without(id));
    };
    return offers.map(({ plan, entry }) => {
        if (away.has(entry.id)) return <p key={`playbook:${entry.id}`} data-playbook-offer-away={entry.id} role="status" className="flex items-center gap-1 rounded-xl border border-border px-4 py-3 text-[13px] text-muted">{t("tasks.notNowAway", { title: entry.title })} ·<Button variant="quiet" size="sm" onClick={() => undoNotNow(entry.id)}>{t("tasks.undo")}</Button></p>;
        const proposal = plan.proposal!;
        const runTime = plan.suggestedTime || proposal.time;
        return (
            <article key={`playbook:${entry.id}`} data-playbook-offer={entry.id} className="flex flex-col rounded-xl border border-border bg-panel px-4 py-4">
                <h3 className="text-[14px] font-semibold leading-snug text-ink">{t("tasks.playbookOffer", { name: entry.title })}</h3>
                {/* The playbook's own description when it has one; otherwise what Isoform saw. */}
                <p className="mt-2 text-[13px] leading-relaxed text-muted">
                    {entry.description?.trim() || <>
                        {t("sopsPage.suggestion.seen", { site: proposal.siteName, time: proposal.time, days: proposal.days })}{" "}
                        {proposal.weekdays ? t("sopsPage.suggestion.offerWeekdays", { runTime }) : t("sopsPage.suggestion.offerDaily", { runTime })}
                    </>}
                </p>
                {proposal.siteName?.trim() && proposal.proposedAt > 0 && (
                    <p className="mt-2 text-xs text-muted">{t("tasks.evidenceCard.source", { source: proposal.siteName, when: new Date(proposal.proposedAt).toLocaleString(i18n.language, { weekday: "short", hour: "numeric", minute: "2-digit", ...computerHourOptions() }) })}</p>
                )}
                {failedLine?.id === entry.id && <FailureLine tone="copper" className="mt-2" text={failedLine.line} />}
                {added.has(entry.id) ? <p data-testid="playbook-added" role="status" className="mt-auto flex items-center gap-1 pt-4 text-[13px] text-muted">{t("tasks.addedToScheduled")} ·<Button variant="quiet" size="sm" disabled={answering !== null} onClick={() => undoAccept(entry.id)}>{t("tasks.undo")}</Button>·<Button variant="quiet" size="sm" disabled={answering !== null} onClick={() => edit(entry)}>{t("tasks.edit")}</Button></p>
                : <div className="mt-auto flex gap-2 pt-4">
                    <Button size="sm" disabled={answering !== null} onClick={() => accept(entry)}>{t("tasks.doIt")}</Button>
                    <Button variant="secondary" size="sm" disabled={answering !== null} onClick={() => notNow(entry)}>{t("tasks.notNow")}</Button>
                </div>}
            </article>
        );
    });
}

function briefIcon(item: UpcomingItem) {
    switch (item.kind) {
        case "brief":
            return item.slot === "evening" ? Moon : Sun;
        default:
            return null;
    }
}

/** Scheduled shows its search and sort once the list is long enough to
 *  need them; the first day's briefs alone never do. */
const SCHEDULED_TOOLS_MIN = 4;

type ScheduledSort = "next" | "name";

/** Tasks › Scheduled (was Upcoming): everything Isoform will do on its own.
 *  Always shows: from setup on, the briefs are here. Upcoming's search, sort
 *  and hover delete carry over. */
function ScheduledSection({ sectionRef, upcoming, listScheduled, onOpenListTask, onSelectSchedule, onOpenBrief, openBrief, onDeleteSchedule }: {
    sectionRef: Ref<HTMLElement>;
    upcoming?: { items: UpcomingItem[]; loading: boolean; failed?: boolean; refresh?: () => void };
    listScheduled: DesktopScheduledTask[];
    onOpenListTask: (id: string) => void;
    onSelectSchedule?: (jobId: string) => void;
    onOpenBrief?: (slot: BriefSlot) => void;
    /** The brief whose detail is open beside the list. */
    openBrief?: BriefSlot | null;
    onDeleteSchedule?: (jobId: string) => Promise<boolean> | void;
}) {
    const { t, i18n } = useTranslation("app");
    const { t: tToday } = useTranslation("today");
    const timeT = (key: string, options?: Record<string, unknown>) => tToday(key as never, options as never) as unknown as string;
    const labels = useMemo(() => createScheduleTextLabels(t), [t]);
    const [query, setQuery] = useState("");
    const [sort, setSort] = useState<ScheduledSort>("next");
    const [pendingDeleteId, setPendingDeleteId] = useState<string | null>(null);
    const [deleteFailed, setDeleteFailed] = useState(false);
    const [deleting, setDeleting] = useState(false);
    const searched = useRef(false);
    const now = new Date();
    const items = (upcoming?.items ?? []).filter((item) => !scheduleFinished(item));
    const loading = (upcoming?.loading ?? true) && items.length === 0;
    const q = query.toLowerCase().trim();
    const listRows = listScheduled.filter((item) => !q || (item.title ?? "").toLowerCase().includes(q));
    let rows = q
        ? items.filter((item) => `${upcomingName(item, t)}\n${upcomingSummary(item, t)}`.toLowerCase().includes(q))
        : items;
    if (sort === "name") rows = [...rows].sort((a, b) => upcomingName(a, t).localeCompare(upcomingName(b, t)));
    const showTools = items.length >= SCHEDULED_TOOLS_MIN || q !== "";
    const sortLabels: Record<ScheduledSort, string> = { next: t("automationsPage.sortNext"), name: t("automationsPage.sortName") };
    const pendingItem = pendingDeleteId ? items.find((item) => item.jobId === pendingDeleteId) : null;
    const openRow = (item: UpcomingItem) => {
        if (item.kind === "brief") { recordTasksAction("T23", "open", "opened"); onOpenBrief?.(item.slot === "evening" ? "evening" : "morning"); }
        else if (item.jobId) { recordTasksAction("T22", "open", "opened"); onSelectSchedule?.(item.jobId); }
    };
    const failed = !!upcoming?.failed;
    useTasksSectionSeen("scheduled", items.length + listScheduled.length, !loading && !failed);
    // The dialog stays open with "Didn't delete. Try again" (T24).
    const confirmDelete = async () => {
        if (!pendingDeleteId || deleting) return;
        setDeleting(true);
        setDeleteFailed(false);
        const deleted = await measureTasks("T24", "do", "done", async () => (await onDeleteSchedule?.(pendingDeleteId)) !== false).catch(() => false);
        setDeleting(false);
        if (deleted) setPendingDeleteId(null);
        else setDeleteFailed(true);
    };
    const tools = showTools ? (
        <div className="flex items-center gap-2">
            <div className="relative">
                <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted" />
                <input type="text" value={query} onChange={(event) => { if (!searched.current && event.target.value) { searched.current = true; recordTasksAction("T20", "do", "done"); } setQuery(event.target.value); }}
                    placeholder={t("automationsPage.searchPlaceholder")} aria-label={t("automationsPage.searchPlaceholder")}
                    className="h-7 w-[180px] rounded-lg border border-border bg-panel pl-8 pr-2.5 text-[12px] text-ink outline-none transition-colors placeholder:text-muted focus:border-border-emphasis" />
            </div>
            <DropdownMenu>
                <DropdownMenuTrigger asChild>
                    <button type="button" aria-label={`${t("automationsPage.sortBy")}: ${sortLabels[sort]}`}
                        className="flex h-7 items-center gap-1 rounded-lg border border-border bg-panel px-2.5 text-[12px] text-muted transition-colors hover:border-border-emphasis">
                        {sortLabels[sort]}
                        <ChevronDown className="size-3 text-muted" />
                    </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="min-w-[150px]">
                    <DropdownMenuLabel className="text-[12px] font-normal text-muted">{t("automationsPage.sortBy")}</DropdownMenuLabel>
                    {(Object.keys(sortLabels) as ScheduledSort[]).map((mode) => (
                        <DropdownMenuItem key={mode} className="flex items-center justify-between text-[13px]" onClick={() => { recordTasksAction("T21", "do", "done"); setSort(mode); }}>
                            <span className={sort === mode ? "font-medium text-ink" : "text-muted"}>{sortLabels[mode]}</span>
                            {sort === mode && <Check className="size-3.5 text-accent" />}
                        </DropdownMenuItem>
                    ))}
                </DropdownMenuContent>
            </DropdownMenu>
        </div>
    ) : undefined;
    return (
        <section ref={sectionRef} aria-label={t("tasks.scheduled")} aria-busy={loading || undefined} data-tasks-section="scheduled" className="scroll-mt-6">
            <TaskSectionHeader title={t("tasks.scheduled")} action={tools} />
            {listRows.length > 0 && <TaskList>{listRows.map((item) => <button key={item.id} type="button" onClick={() => onOpenListTask(item.id)} className="flex w-full items-center gap-3 px-4 py-3 text-left"><Clock className="size-4 shrink-0 text-muted" /><span className="min-w-0"><span className="block text-sm font-medium text-ink">{item.title}</span><span className="block text-xs text-muted">{item.at && !Number.isNaN(Date.parse(item.at)) ? new Intl.DateTimeFormat(i18n.language, { dateStyle: "medium", timeStyle: "short" }).format(new Date(item.at)) : t("tasks.brainDumpReminder")}</span></span></button>)}</TaskList>}
            {failed && <FailureLine testId="scheduled-failed" className="mb-2" text={t("tasks.failure.scheduled")} onRetry={() => { recordTasksAction("T25", "do", "done"); upcoming?.refresh?.(); }} />}
            {failed && items.length === 0 ? null : loading && listScheduled.length === 0 ? <TaskRowPlaceholders /> : items.length === 0 && listScheduled.length === 0 ? (
                <p className="text-[13px] text-muted">{t("automationsPage.emptyHint")}</p>
            ) : rows.length === 0 && listRows.length === 0 ? (
                <p className="text-[13px] text-muted">{t("automationsPage.emptySearch")}</p>
            ) : rows.length === 0 ? null : (
                <TaskList>
                    {rows.map((item) => {
                        const name = upcomingName(item, t);
                        const Icon = briefIcon(item);
                        const failing = item.kind !== "brief" && item.enabled && scheduleKeptFailing(item);
                        const isBrief = item.kind === "brief";
                        // A brief's row is its title and when (tasks-01), in the detail's words.
                        const when = !item.enabled ? t("automationsPage.paused") : isBrief ? briefEvery(item.time, i18n.language, t, timeT) : upcomingWhen(item, now, t, timeT, labels);
                        const next = !item.enabled ? "" : isBrief ? briefNext(item.nextRunAt, now, i18n.language, t, timeT) : upcomingNextLabel(item, now, t, timeT);
                        const didntRunAt = isBrief && item.enabled && (item.didntRunAt ?? 0) > 0 ? new Date(item.didntRunAt!) : null;
                        const canDelete = item.kind === "schedule" && !item.system && !!item.jobId && !!onDeleteSchedule;
                        return (
                            <div key={item.id} data-scheduled-row={item.id} data-selected={item.kind === "brief" && openBrief === (item.slot === "evening" ? "evening" : "morning") || undefined} className="group/scheduled flex items-start transition-colors hover:bg-surface-hover data-[selected]:bg-accent-light">
                                <button type="button" onClick={() => openRow(item)} className="flex min-w-0 flex-1 items-start gap-3 py-3 pl-4 pr-2 text-left">
                                    {Icon
                                        ? <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-lg bg-surface-active text-muted"><Icon className="size-3.5" /></span>
                                        : <span className="mt-0.5 shrink-0"><AutomationAppIcon appName={name} size={28} dimmed={!item.enabled} /></span>}
                                    <span className="min-w-0 flex-1">
                                        <span className={`block truncate text-[14px] font-medium ${item.enabled ? "text-ink" : "text-muted"}`}>{name}</span>
                                        {!isBrief && <span className="mt-0.5 block truncate text-[12px] text-muted">{upcomingSummary(item, t)}</span>}
                                        {didntRunAt && <span data-brief-didnt-run className="mt-1 block text-[12px] text-muted">{t("tasks.didntRunAt", { time: clockTime(didntRunAt, i18n.language, timeT) })}</span>}
                                        {failing && <span data-schedule-failing className="mt-1 block text-[12px] text-copper">{t("tasks.didntRun")}</span>}
                                    </span>
                                    <span className="shrink-0 text-right text-[12px] leading-[18px] text-muted">
                                        {when && <span className="block">{when}</span>}
                                        {next && <span className="block">{next}</span>}
                                    </span>
                                </button>
                                <span className="flex w-9 shrink-0 justify-center pt-3.5">
                                    {canDelete && (
                                        <button type="button" data-scheduled-delete={item.id} aria-label={`${t("automationsPage.delete")} ${name}`} title={t("automationsPage.delete")}
                                            onClick={() => { setDeleteFailed(false); setPendingDeleteId(item.jobId); }}
                                            className="flex size-6 items-center justify-center rounded text-muted opacity-0 transition-all hover:bg-error/10 hover:text-error focus-visible:opacity-100 group-hover/scheduled:opacity-100">
                                            <X className="size-3.5" />
                                        </button>
                                    )}
                                </span>
                            </div>
                        );
                    })}
                </TaskList>
            )}
            <ConfirmDialog
                open={!!pendingDeleteId}
                onOpenChange={(open) => { if (!open) { setPendingDeleteId(null); setDeleteFailed(false); } }}
                title={t("deleteAutomationDialog.title")}
                description={
                    <>
                        {t("deleteAutomationDialog.confirmStart")}{" "}
                        <span className="font-medium text-ink">{(pendingItem && upcomingName(pendingItem, t)) || t("deleteAutomationDialog.fallbackName")}</span>
                        {t("deleteAutomationDialog.confirmEnd")}
                    </>
                }
                confirmLabel={t("deleteAutomationDialog.delete")}
                variant="danger"
                closeOnConfirm={false}
                confirmDisabled={deleting}
                notice={deleteFailed ? <FailureLine tone="copper" testId="scheduled-delete-failed" text={t("tasks.failure.delete")} onRetry={() => void confirmDelete()} /> : undefined}
                onConfirm={() => void confirmDelete()}
            />
        </section>
    );
}

function DoneTaskRow({ row, language, surface }: { row: DoneRow; language: string; surface: "board" | "all" }) {
    const { t } = useTranslation("app");
    const files = row.entry?.files ?? [];
    const entry = row.entry;
    // "Couldn't open {file}. Try again" under the chip (T27, D02), never a toast.
    const [failedFile, setFailedFile] = useState<{ file: DeliverableFileRef; reveal: boolean } | null>(null);
    const fileAction: TasksActionId = surface === "all" ? "D02" : "T27";
    const openFile = (file: DeliverableFileRef, reveal: boolean) => {
        if (!entry) return;
        setFailedFile(null);
        void measureTasks(fileAction, "open", "opened", async () => reveal ? RevealDeliverableFile(entry.source, entry.sourceId, file.path) : OpenDeliverableFile(entry.source, entry.sourceId, file.path)).catch(() => setFailedFile({ file, reveal }));
    };
    const day = new Date(row.at).toLocaleDateString(language, { weekday: "short" });
    return (
        <div className="flex items-start gap-3 px-4 py-3">
            <CheckCircle2 data-done-check={row.byYou ? "you" : "isoform"} className={row.byYou ? "mt-0.5 size-[18px] shrink-0 text-muted" : "mt-0.5 size-[18px] shrink-0 text-success"} />
            <div className="min-w-0 flex-1">
                {row.open
                    ? <button className="block max-w-full truncate text-left text-[14px] font-medium text-ink hover:underline" onClick={() => row.open?.(surface === "all" ? "D02" : "T26")}>{row.title}</button>
                    : <span className="block truncate text-[14px] font-medium text-ink">{row.title}</span>}
                {row.outcome && <p className="mt-1 text-xs text-muted">{row.outcome}</p>}
                {entry && files.length > 0 && (
                    <div className="mt-1.5 flex flex-wrap gap-1.5">
                        {files.slice(0, 4).map((file: DeliverableFileRef) => (
                            <ContextMenu key={file.path}>
                                <ContextMenuTrigger asChild>
                                    <button type="button" title={file.path} data-done-file={file.name}
                                        onClick={() => openFile(file, false)}
                                        className="inline-flex max-w-[220px] items-center gap-1 rounded-md border border-border bg-surface-active px-1.5 py-0.5 text-[12px] text-ink hover:bg-surface-hover">
                                        <FileText className="size-3 shrink-0 text-muted" />
                                        <span className="truncate">{file.name}</span>
                                    </button>
                                </ContextMenuTrigger>
                                <ContextMenuContent>
                                    <ContextMenuItem onSelect={() => openFile(file, true)}>{t("tasks.showInFinder")}</ContextMenuItem>
                                </ContextMenuContent>
                            </ContextMenu>
                        ))}
                    </div>
                )}
                {failedFile && <FailureLine className="mt-1.5" testId="done-file-failed" text={t("tasks.failure.openFile", { file: failedFile.file.name })} onRetry={() => openFile(failedFile.file, failedFile.reveal)} />}
            </div>
            <span className="shrink-0 text-[12px] text-muted">{day}</span>
        </div>
    );
}

/** Never read yet (a first run): two static rows in the shape of real ones,
 *  never "Loading…" and never a group that wrongly says it is empty. */
function TaskRowPlaceholders() {
    return (
        <div data-testid="task-row-placeholder" aria-hidden className="flex flex-col divide-y divide-border-divider rounded-xl border border-border bg-panel">
            {[0, 1].map((row) => (
                <div key={row} className="flex items-center gap-3 px-4 py-3">
                    <span className="size-7 shrink-0 rounded-lg bg-surface-active" />
                    <span className="flex min-w-0 flex-1 flex-col gap-2">
                        <span className="h-3 w-2/5 rounded-md bg-surface-active" />
                        <span className="h-3 w-3/5 rounded-md bg-surface-active" />
                    </span>
                </div>
            ))}
        </div>
    );
}

function TaskProgress({ progress }: { progress?: HandoffEntry["progress"] }) {
    const { t } = useTranslation("app");
    const known = progress && progress.total > 0;
    const fraction = known ? Math.max(0, Math.min(1, progress.done / progress.total)) : 0;
    return (
        <svg role="progressbar" aria-label={t("tasks.running")} aria-valuemin={0} aria-valuemax={known ? progress.total : undefined} aria-valuenow={known ? progress.done : undefined} viewBox="0 0 36 36" className={`size-7 shrink-0 text-accent ${known ? "" : "animate-spin motion-reduce:animate-none"}`}>
            <circle cx="18" cy="18" r="14" fill="none" stroke="currentColor" strokeWidth="3" className="opacity-20" />
            <circle cx="18" cy="18" r="14" fill="none" stroke="currentColor" strokeWidth="3" pathLength="1" strokeDasharray={`${known ? fraction : 0.25} 1`} strokeLinecap="round" transform="rotate(-90 18 18)" />
        </svg>
    );
}

function OpenAnswerTaskRow({ answer, onOpen }: { answer: import("@/lib/open-answers").OpenAnswer; onOpen: () => void }) {
    const { t, i18n } = useTranslation("app");
    const [busy, setBusy] = useState(false);
    const busyRef = useRef(false);
    const [error, setError] = useState(false);
    const language = i18n.resolvedLanguage ?? i18n.language;
    const time = briefClockTime(answer.windowStart, language, (key, options) => String(appI18n.t(key as never, { ...options, ns: "today", lng: language }))) ?? "";
    const line = answer.delivery === "around" && answer.windowStart
        ? t("tasks.answerAround", { time })
        : answer.delivery === "brief"
            ? t("tasks.answerBrief")
            : answer.settles === "reply" && answer.watchedEvent?.who
                ? t("tasks.answerReply", { who: answer.watchedEvent.who })
                : t("tasks.answerSettled");
    const change = async (delivery: OpenAnswerDelivery) => {
        if (busyRef.current) return;
        const requestGeneration = getOpenAnswersGeneration();
        busyRef.current = true;
        setBusy(true);
        setError(false);
        try {
            const returned = await measureTasks("T18", "do", "done", () => ChangeOpenAnswer(answer.id, delivery));
            const next = returned ? parseOpenAnswer(returned) : undefined;
            if (next) updateOpenAnswer(next, requestGeneration);
            else await refreshOpenAnswers(requestGeneration);
        } catch {
            setError(true);
        } finally {
            busyRef.current = false;
            setBusy(false);
        }
    };
    const cancel = async () => {
        if (busyRef.current) return;
        const requestGeneration = getOpenAnswersGeneration();
        busyRef.current = true;
        setBusy(true);
        setError(false);
        try {
            const returned = await measureTasks("T19", "do", "done", () => CancelOpenAnswer(answer.id));
            const next = returned ? parseOpenAnswer(returned) : undefined;
            if (next) updateOpenAnswer(next, requestGeneration);
            else await refreshOpenAnswers(requestGeneration);
        } catch {
            setError(true);
        } finally {
            busyRef.current = false;
            setBusy(false);
        }
    };
    return <div className="flex w-full items-center gap-3 px-4 py-3 text-left">
        <button className="flex min-w-0 flex-1 items-center gap-3 text-left" onClick={onOpen}>
            <span className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-surface-active text-muted"><Clock className="size-3.5" /></span>
            <span className="min-w-0"><span className="block truncate text-[14px] font-medium text-ink">{answer.question}</span><span className="mt-0.5 block text-[12px] text-muted">{line}</span></span>
        </button>
        <DropdownMenu>
            <DropdownMenuTrigger asChild><Button size="icon" variant="quiet" aria-label={t("tasks.more")}><MoreHorizontal className="size-4" /></Button></DropdownMenuTrigger>
            <DropdownMenuContent align="end">
                <DropdownMenuLabel>{t("tasks.answerChange")}</DropdownMenuLabel>
                <DropdownMenuSeparator />
                {answer.windowStart && <DropdownMenuItem disabled={busy} onSelect={() => void change("around")}>{t("tasks.answerAroundChoice", { time })}</DropdownMenuItem>}
                <DropdownMenuItem disabled={busy} onSelect={() => void change("settled_only")}>{t("tasks.answerSettledChoice")}</DropdownMenuItem>
                <DropdownMenuItem disabled={busy} onSelect={() => void change("brief")}>{t("tasks.answerBrief")}</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem disabled={busy} onSelect={() => void cancel()}>{t("tasks.answerNoNeed")}</DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
        {error && <FailureLine tone="copper" text={t("tasks.answerActionError")} />}
    </div>;
}
