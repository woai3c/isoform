import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowLeft, CheckCircle2, Circle, MoreHorizontal, Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogCloseButton, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { GetGoal, MoveGoal, type GoalItemView, type GoalStoryView } from "@/lib/wails-shim-app";
import { useGoals } from "@/hooks/useGoals";
import { usePagePrivacyWithdrawal } from "@/lib/page-queries";

export type Translate = (key: string, options?: Record<string, unknown>) => string;

// A fresh key per click; the computer ignores a repeated key.
function newKey(): string {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
    return `${Date.now().toString(16)}-${Math.random().toString(16).slice(2, 10)}`;
}

function errorKey(error: unknown): string {
    const text = String(error);
    if (text.includes("goal_changed")) return "goalsPage.errors.changed";
    if (text.includes("goal_invalid")) return "goalsPage.errors.invalid";
    return "goalsPage.errors.unavailable";
}

function localDay(date: Date): number {
    return Date.UTC(date.getFullYear(), date.getMonth(), date.getDate());
}

/** "today", "yesterday", "on Tuesday" within the week, then "on October 2";
 *  after "since" (bare) without the "on". */
export function goalDayPhrase(iso: string | undefined, now: Date, language: string, t: Translate, bare = false): string | null {
    const at = iso ? new Date(iso) : null;
    if (!at || Number.isNaN(at.getTime())) return null;
    const days = Math.round((localDay(now) - localDay(at)) / 86_400_000);
    if (days <= 0) return t("goalsPage.day.today");
    if (days === 1) return t("goalsPage.day.yesterday");
    const day = days < 7
        ? at.toLocaleDateString(language, { weekday: language.startsWith("zh") ? "short" : "long" })
        : at.toLocaleDateString(language, { month: "long", day: "numeric", ...(at.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }) });
    return bare ? day : t("goalsPage.day.on", { day });
}

/** The line under a goal, in the user's language, from the facts Go sends. */
export function goalStatus(goal: GoalItemView, now: Date, language: string, t: Translate): string {
    const day = (iso?: string) => goalDayPhrase(iso, now, language, t) ?? "";
    const since = (iso?: string) => goalDayPhrase(iso, now, language, t, true) ?? "";
    if (goal.state === "resolved") return t("goalsPage.status.done", { day: day(goal.updatedAt) });
    const parts: string[] = [];
    if (goal.state === "waiting") {
        parts.push(goal.waitingOn
            ? t("goalsPage.status.waitingOn", { who: goal.waitingOn, day: since(goal.updatedAt) })
            : t("goalsPage.status.waiting", { day: since(goal.updatedAt) }));
    }
    const due = goal.dueAt ? new Date(goal.dueAt) : null;
    if (due && !Number.isNaN(due.getTime())) {
        const date = due.toLocaleDateString(language, { month: "short", day: "numeric" });
        const days = Math.round((localDay(due) - localDay(now)) / 86_400_000);
        parts.push(days < 0 ? t("goalsPage.status.overdue", { date }) : days === 0 ? t("goalsPage.status.dueToday") : t("goalsPage.status.due", { date }));
    }
    if (goal.state !== "waiting") {
        if (goal.lastEvent === "discovered") parts.push(t(`goalsPage.status.added.${goal.origin}`, { day: day(goal.addedAt) }));
        else if (goal.lastEvent === "reopened") parts.push(t("goalsPage.status.reopened", { day: day(goal.updatedAt) }));
        else parts.push(t("goalsPage.status.updated", { day: day(goal.updatedAt) }));
    }
    return parts.join(" ");
}

export interface GoalsBoardOptions {
    /** A goal to open (Search, a notice); a new signal opens it again. */
    openGoalId?: string | null;
    openGoalSignal?: number;
    /** Called once the request is taken, so a later visit opens the list. */
    onOpenGoalHandled?: () => void;
}

type PendingGoalMove = { goal: GoalItemView; action: "resolve" | "forget"; eventId?: string; undoFailed?: boolean; undoing?: boolean };
const INLINE_UNDO_MS = 5_000;

/** The goal lists with their moves, the open story and the waiting and edit
 *  dialogs. The Goals page and About you both run on it; moves go through
 *  MoveGoal, the same path as Today and the phone. */
export function useGoalsBoard({ openGoalId, openGoalSignal, onOpenGoalHandled }: GoalsBoardOptions) {
    const { t } = useTranslation("app");
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const { page, loading, failed, refresh } = useGoals(true);
    // A goal marked done or forgotten leaves its list at once, by the event the
    // page showed: the next read shows a done goal under Done, and Undo brings
    // either back.
    const [hidden, setHidden] = useState<ReadonlyMap<string, string>>(() => new Map());
    const pageRef = useRef(page);
    pageRef.current = page;
    const onHidden = useCallback((goalId: string, hide: boolean) => {
        setHidden((current) => {
            const next = new Map(current);
            if (hide) {
                const shown = [...(pageRef.current?.tracking ?? []), ...(pageRef.current?.done ?? []), ...(pageRef.current?.held ?? [])]
                    .find((goal) => goal.id === goalId);
                next.set(goalId, shown?.eventId ?? "");
            } else {
                next.delete(goalId);
            }
            return next;
        });
    }, []);
    const [pendingMoves, setPendingMoves] = useState<Record<string, PendingGoalMove>>({});
    const moveTimers = useRef(new Map<string, ReturnType<typeof setTimeout>>());
    const moveGeneration = useRef(0);
    const busyMoves = useRef(new Set<string>());
    const undoingMoves = useRef(new Set<string>());
    const clearMoves = useCallback(() => {
        moveGeneration.current++;
        moveTimers.current.forEach(clearTimeout);
        moveTimers.current.clear();
        busyMoves.current.clear();
        undoingMoves.current.clear();
        setPendingMoves({});
    }, []);
    useEffect(() => () => {
        moveGeneration.current++;
        moveTimers.current.forEach(clearTimeout);
        moveTimers.current.clear();
    }, []);
    const [openId, setOpenId] = useState<string | null>(openGoalId ?? null);
    useEffect(() => {
        if (!openGoalId) return;
        setOpenId(openGoalId);
        onOpenGoalHandled?.();
        // A new request is a new id or signal; the callback is not one.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [openGoalId, openGoalSignal]);
    const [waitingFor, setWaitingFor] = useState<GoalItemView | null>(null);
    const [editing, setEditing] = useState<GoalItemView | null>(null);

    // A privacy transition closes anything holding a goal's text — the open
    // story, the waiting/edit dialogs — and forgets the hidden-row overlay;
    // useGoals withdraws the lists themselves.
    usePagePrivacyWithdrawal(useCallback(() => {
        setHidden(new Map());
        setOpenId(null);
        setWaitingFor(null);
        setEditing(null);
        clearMoves();
    }, [clearMoves]));

    const isHidden = (goal: GoalItemView) => hidden.has(goal.id) && hidden.get(goal.id) === goal.eventId;
    const visible = (items: GoalItemView[] | undefined, group: GoalItemView["group"]) => {
        const result = (items ?? []).filter((goal) => !pendingMoves[goal.id] && !isHidden(goal));
        return [...result, ...Object.values(pendingMoves).filter((move) => move.goal.group === group).map((move) => move.goal)];
    };
    const tracking = visible(page?.tracking, "tracking");
    const done = visible(page?.done, "done");
    const held = visible(page?.held, "held");
    const all = useMemo(() => [...(page?.tracking ?? []), ...(page?.done ?? []), ...(page?.held ?? [])], [page]);

    const plainMove = async (goal: GoalItemView, action: "reopen" | "waiting" | "revise", extra: { waitingOn?: string; title?: string; dueDate?: string; setDue?: boolean } = {}) => {
        try {
            await MoveGoal({ goalId: goal.id, expectedEventId: goal.eventId, action, key: newKey(), ...extra });
            refresh();
            return true;
        } catch (error) {
            toast.error(tr(errorKey(error)));
            refresh();
            return false;
        }
    };
    const move = async (goal: GoalItemView, action: "resolve" | "forget") => {
        if (busyMoves.current.has(goal.id)) return false;
        busyMoves.current.add(goal.id);
        const generation = moveGeneration.current;
        setPendingMoves((current) => ({ ...current, [goal.id]: { goal, action } }));
        try {
            const result = await MoveGoal({ goalId: goal.id, expectedEventId: goal.eventId, action, key: newKey() });
            if (generation !== moveGeneration.current) return true;
            setHidden((current) => new Map(current).set(goal.id, goal.eventId));
            setPendingMoves((current) => ({ ...current, [goal.id]: { goal, action, eventId: result.eventId } }));
            moveTimers.current.set(goal.id, setTimeout(() => {
                moveTimers.current.delete(goal.id);
                busyMoves.current.delete(goal.id);
                setPendingMoves((current) => { const next = { ...current }; delete next[goal.id]; return next; });
                refresh();
            }, INLINE_UNDO_MS));
            return true;
        } catch (error) {
            if (generation !== moveGeneration.current) return false;
            busyMoves.current.delete(goal.id);
            setPendingMoves((current) => { const next = { ...current }; delete next[goal.id]; return next; });
            toast.error(tr(errorKey(error)));
            refresh();
            return false;
        }
    };
    const undoMove = async (id: string) => {
        const pending = pendingMoves[id];
        if (!pending?.eventId || undoingMoves.current.has(id)) return;
        undoingMoves.current.add(id);
        const generation = moveGeneration.current;
        const timer = moveTimers.current.get(id);
        if (timer) clearTimeout(timer);
        moveTimers.current.delete(id);
        setPendingMoves((current) => current[id] ? { ...current, [id]: { ...current[id], undoing: true } } : current);
        try {
            await MoveGoal({ goalId: id, expectedEventId: pending.eventId, action: "restore", key: newKey() });
            if (generation !== moveGeneration.current) return;
            onHidden(id, false);
            busyMoves.current.delete(id);
            setPendingMoves((current) => { const next = { ...current }; delete next[id]; return next; });
            refresh();
        } catch {
            if (generation !== moveGeneration.current) return;
            setPendingMoves((current) => current[id] ? { ...current, [id]: { ...current[id], undoFailed: true, undoing: false } } : current);
        } finally {
            undoingMoves.current.delete(id);
        }
    };
    const toggle = (goal: GoalItemView) => {
        if (goal.state === "resolved") void plainMove(goal, "reopen");
        else void move(goal, "resolve");
    };
    const forget = async (goal: GoalItemView) => {
        if (await move(goal, "forget") && openId === goal.id) setOpenId(null);
    };
    const actions: GoalActions = {
        toggle,
        waiting: (goal) => setWaitingFor(goal),
        edit: (goal) => setEditing(goal),
        forget: (goal) => void forget(goal),
        open: (goal) => setOpenId(goal.id),
    };

    const opened = openId ? all.find((goal) => goal.id === openId && !isHidden(goal)) : undefined;
    return {
        page, loading, failed, refresh, tracking, done, held, all, opened, openId, setOpenId, actions,
        waitingFor, setWaitingFor, editing, setEditing, plainMove, pendingMoves, undoMove,
    };
}

export type GoalsBoard = ReturnType<typeof useGoalsBoard>;

/** The computer's Goals page (item 66): the goals the phone tracks, the ones
 *  done, and the ones kept on this computer, each with the moves the phone
 *  has plus edit; a goal opens its story. Moves go through MoveGoal, the same
 *  path as Today and the phone. */
export function GoalsPage({ onTalkAbout, onCreateGoal, wideMode = false, now: providedNow, openGoalId, openGoalSignal, onOpenGoalHandled }: {
    /** Opens the main chat about this goal, with the goal attached. */
    onTalkAbout: (goal: GoalItemView, prompt: string) => void;
    /** Opens the main chat with a goal to finish in the composer. */
    onCreateGoal: (prompt: string) => void;
    wideMode?: boolean;
    now?: Date;
    /** A goal to open on (Search); a new signal opens it again. */
    openGoalId?: string | null;
    openGoalSignal?: number;
    /** Called once the request is taken, so a later visit opens the list. */
    onOpenGoalHandled?: () => void;
}) {
    const { t, i18n } = useTranslation("app");
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const language = i18n.resolvedLanguage ?? i18n.language;
    const now = providedNow ?? new Date();
    const board = useGoalsBoard({ openGoalId, openGoalSignal, onOpenGoalHandled });
    const { page, loading, failed, refresh, tracking, done, held, opened, actions } = board;

    return (
        <div className="flex min-h-0 flex-1 flex-col" data-testid="goals-page">
            <div className="flex-1 overflow-y-auto">
                <div aria-hidden className="window-drag h-12" />
                <div className={cn("animate-rise-in mx-auto w-full px-8 pb-16 motion-reduce:animate-none", wideMode ? "max-w-none" : "max-w-[720px]")}>
                    {opened ? (
                        <GoalStory goal={opened} now={now} language={language} t={tr} actions={actions}
                            onBack={() => board.setOpenId(null)} onTalkAbout={() => onTalkAbout(opened, tr("goalsPage.story.talkPrompt", { title: opened.title }))} />
                    ) : (
                        <>
                            <h1 className="window-drag mb-[38px] text-[36px] font-medium text-ink">{tr("goalsPage.title")}</h1>
                            {!page && loading && (
                                // Never read yet: static goal rows, never a spinner
                                // (snappy-and-first-run-1.0.md, section 2).
                                <div data-testid="goals-placeholder" aria-busy="true" aria-label={tr("goalsPage.loading")} className="mb-6 flex flex-col">
                                    {[0, 1, 2].map((row) => (
                                        <div key={row} className="flex flex-col gap-2 border-b border-border-divider py-4 last:border-0">
                                            <span className="h-3.5 w-2/5 rounded-md bg-surface-active" />
                                            <span className="h-3 w-3/5 rounded-md bg-surface-active" />
                                        </div>
                                    ))}
                                </div>
                            )}
                            {failed && (
                                <div className="mb-6 flex items-center gap-3 text-[13px] text-muted">
                                    <span>{tr("goalsPage.loadFailed")}</span>
                                    <Button size="sm" variant="secondary" onClick={refresh}>{tr("goalsPage.retry")}</Button>
                                </div>
                            )}
                            {page && tracking.length === 0 && done.length === 0 && held.length === 0 && (
                                <p className="mb-6 text-[13px] text-muted" data-testid="goals-empty">{tr("goalsPage.empty")}</p>
                            )}
                            <GoalList name="tracking" label={tracking.length ? tr("goalsPage.tracking") : null} goals={tracking} now={now} language={language} t={tr} actions={actions} board={board} />
                            <GoalList name="done" label={done.length ? tr("goalsPage.done", { count: done.length }) : null} goals={done} now={now} language={language} t={tr} actions={actions} board={board} />
                            <GoalList name="held" label={held.length ? tr("goalsPage.held", { count: held.length }) : null} goals={held} now={now} language={language} t={tr} actions={actions} board={board} held />
                            <button
                                type="button"
                                data-testid="goals-create"
                                className="mt-2 inline-flex items-center gap-1.5 text-[13px] font-medium text-muted transition-colors hover:text-ink"
                                onClick={() => onCreateGoal(tr("goalsPage.createPrompt"))}
                            >
                                <Plus className="size-3.5" />
                                {tr("goalsPage.create")}
                            </button>
                        </>
                    )}
                </div>
            </div>
            <GoalDialogs board={board} t={tr} />
        </div>
    );
}

/** The waiting and edit dialogs a goal's menu opens. The Goals page and About
 *  you both render them over the same board. */
export function GoalDialogs({ board, t }: { board: GoalsBoard; t: Translate }) {
    return (
        <>
            <WaitingDialog goal={board.waitingFor} t={t} onClose={() => board.setWaitingFor(null)}
                onSave={(goal, who) => board.plainMove(goal, "waiting", { waitingOn: who })} />
            <EditDialog goal={board.editing} t={t} onClose={() => board.setEditing(null)}
                onSave={(goal, title, due) => board.plainMove(goal, "revise", { title, ...(due === undefined ? {} : { dueDate: due, setDue: true }) })} />
        </>
    );
}

export interface GoalActions {
    toggle: (goal: GoalItemView) => void;
    waiting: (goal: GoalItemView) => void;
    edit: (goal: GoalItemView) => void;
    forget: (goal: GoalItemView) => void;
    open: (goal: GoalItemView) => void;
}

export function GoalInlineResult({ pending, undo, t }: { pending: PendingGoalMove; undo: () => void; t: Translate }) {
    return <span className="flex flex-wrap items-center gap-1 text-[12px] text-muted" role="status">
        {t(pending.action === "resolve" ? "goalsPage.inlineDone" : "goalsPage.inlineForgot")}
        <span aria-hidden="true"> · </span>
        <button type="button" className="text-accent hover:underline" disabled={!pending.eventId || pending.undoing} onClick={undo}>{t("goalsPage.inlineUndo")}</button>
        {pending.undoFailed && <span role="alert">{t("goalsPage.inlineUndoFailed")}</span>}
    </span>;
}

function GoalList({ name, label, goals, now, language, t, actions, board, held = false }: {
    name: string;
    label: string | null;
    goals: GoalItemView[];
    now: Date;
    language: string;
    t: Translate;
    actions: GoalActions;
    board: GoalsBoard;
    held?: boolean;
}) {
    if (goals.length === 0) return null;
    return (
        <section className="mb-8" data-testid={`goals-${name}`}>
            {label && <div className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-muted/70">{label}</div>}
            <ul className="divide-y divide-border rounded-2xl border border-border bg-background">
                {goals.map((goal) => (
                    <li key={goal.id} className="group flex items-start gap-3 px-4 py-3" data-testid="goal-row">
                        {board.pendingMoves[goal.id] ? <div className="min-w-0 flex-1"><span className="block text-[13px] text-ink">{goal.title}</span><GoalInlineResult pending={board.pendingMoves[goal.id]} undo={() => void board.undoMove(goal.id)} t={t} /></div> : <>
                        <button
                            type="button"
                            className="mt-0.5 shrink-0 text-muted transition-colors hover:text-ink"
                            aria-label={t(goal.state === "resolved" ? "goalsPage.reopenLabel" : "goalsPage.checkLabel", { title: goal.title })}
                            onClick={() => actions.toggle(goal)}
                        >
                            {goal.state === "resolved" ? <CheckCircle2 className="size-4" /> : <Circle className="size-4" />}
                        </button>
                        <button type="button" className="min-w-0 flex-1 text-left" onClick={() => actions.open(goal)}>
                            <span className={cn("block break-words text-[13px] font-medium", goal.state === "resolved" ? "text-muted" : "text-ink")}>{goal.title}</span>
                            <span className={cn("mt-0.5 block text-[12px] leading-snug", goal.slipping ? "text-accent" : "text-muted")}>
                                {held ? t("goalsPage.heldReason") : goalStatus(goal, now, language, t)}
                            </span>
                        </button>
                        <GoalMenu goal={goal} t={t} actions={actions} />
                        </>}
                    </li>
                ))}
            </ul>
        </section>
    );
}

export function GoalMenu({ goal, t, actions }: { goal: GoalItemView; t: Translate; actions: GoalActions }) {
    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <button
                    type="button"
                    data-testid="goal-menu"
                    aria-label={t("goalsPage.more", { title: goal.title })}
                    className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted opacity-0 transition-[opacity,color,background-color] hover:bg-ink/10 hover:text-ink focus-visible:opacity-100 group-hover:opacity-100 group-focus-within:opacity-100 data-[state=open]:opacity-100"
                >
                    <MoreHorizontal className="size-3.5" />
                </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" sideOffset={4}>
                <GoalMenuItems goal={goal} t={t} actions={actions} Item={MenuItem} />
            </DropdownMenuContent>
        </DropdownMenu>
    );
}

/** The moves a goal offers, as menu items or as buttons on its story. */
function GoalMenuItems({ goal, t, actions, Item }: {
    goal: GoalItemView;
    t: Translate;
    actions: GoalActions;
    Item: React.ComponentType<{ onClick: () => void; children: React.ReactNode }>;
}) {
    if (goal.state === "resolved") {
        return <Item onClick={() => actions.toggle(goal)}>{t("goalsPage.reopen")}</Item>;
    }
    return (
        <>
            <Item onClick={() => actions.toggle(goal)}>{t("goalsPage.markDone")}</Item>
            {goal.state !== "waiting" && <Item onClick={() => actions.waiting(goal)}>{t("goalsPage.waiting")}</Item>}
            <Item onClick={() => actions.edit(goal)}>{t("goalsPage.edit")}</Item>
            <Item onClick={() => actions.forget(goal)}>{t("goalsPage.forget")}</Item>
        </>
    );
}

function MenuItem({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
    return <DropdownMenuItem onClick={onClick}>{children}</DropdownMenuItem>;
}

function StoryButton({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
    return <Button size="sm" variant="secondary" onClick={onClick}>{children}</Button>;
}

/** The ledger events the story names; any other is "Changed". */
const STORY_EVENTS = new Set(["discovered", "reinforced", "revised", "waiting", "resolved", "reopened", "dismissed"]);

export function GoalStory({ goal, now, language, t, actions, onBack, onTalkAbout }: {
    goal: GoalItemView;
    now: Date;
    language: string;
    t: Translate;
    actions: GoalActions;
    onBack: () => void;
    onTalkAbout?: () => void;
}) {
    const [story, setStory] = useState<GoalStoryView | null>(null);
    const [storyFailed, setStoryFailed] = useState(false);
    useEffect(() => {
        let live = true;
        setStoryFailed(false);
        GetGoal(goal.id)
            .then((next) => { if (live) setStory(next); })
            .catch(() => {
                if (!live) return;
                setStory(null);
                setStoryFailed(true);
            });
        return () => { live = false; };
    }, [goal.id, goal.eventId]);
    const timeline = [...(story?.history ?? [])].reverse().map((entry) => {
        const at = new Date(entry.at);
        const date = Number.isNaN(at.getTime()) ? "" : at.toLocaleDateString(language, { month: "short", day: "numeric" });
        const label = t(`goalsPage.story.event.${STORY_EVENTS.has(entry.event) ? entry.event : "other"}`);
        return `${label} ${date}`.trim();
    });
    const sources = (story?.sources ?? []).map((source) => t("goalsPage.story.source", { label: t(`goalsPage.story.sourceKind.${source.kind}`), count: source.count }));
    return (
        <div data-testid="goal-story">
            <button type="button" className="window-drag mb-6 inline-flex items-center gap-1.5 text-[13px] font-medium text-muted transition-colors hover:text-ink" onClick={onBack}>
                <ArrowLeft className="size-3.5" />
                {t("userMenu.memory")}
            </button>
            <h1 className="break-words text-[26px] font-semibold text-ink">{goal.title}</h1>
            <p className={cn("mt-1 text-[13px] leading-snug", goal.slipping ? "text-accent" : "text-muted")}>
                {goal.group === "held" ? t("goalsPage.heldReason") : goalStatus(goal, now, language, t)}
            </p>
            {goal.outcome && <p className="mt-4 text-[13px] leading-relaxed text-ink/85">{t("goalsPage.story.aim", { outcome: goal.outcome })}</p>}
            {storyFailed && <p className="mt-4 text-[12px] text-muted" data-testid="goal-story-failed">{t("goalsPage.story.loadFailed")}</p>}
            {timeline.length > 0 && (
                <p className="mt-4 text-[12px] text-muted" data-testid="goal-timeline">{timeline.join(" · ")}</p>
            )}
            {sources.length > 0 && (
                <p className="mt-1 text-[12px] text-muted" data-testid="goal-sources">{t("goalsPage.story.restsOn", { list: sources.join(", ") })}</p>
            )}
            <div className="mt-6 flex flex-wrap items-center gap-2">
                <GoalMenuItems goal={goal} t={t} actions={actions} Item={StoryButton} />
                {onTalkAbout && (
                    <button type="button" className="ml-2 text-[12px] font-medium text-muted transition-colors hover:text-ink" onClick={onTalkAbout}>
                        {t("goalsPage.story.talk")}
                    </button>
                )}
            </div>
        </div>
    );
}

function WaitingDialog({ goal, t, onClose, onSave }: {
    goal: GoalItemView | null;
    t: Translate;
    onClose: () => void;
    /** Resolves true when saved; the dialog and its text stay on a failure. */
    onSave: (goal: GoalItemView, who: string) => Promise<boolean>;
}) {
    const [who, setWho] = useState("");
    useEffect(() => setWho(""), [goal?.id]);
    return (
        <Dialog open={!!goal} onOpenChange={(open) => { if (!open) onClose(); }}>
            <DialogContent className="w-[400px]">
                <DialogHeader>
                    <DialogTitle className="text-sm">{t("goalsPage.waitingDialog.title")}</DialogTitle>
                    <DialogCloseButton />
                </DialogHeader>
                <form
                    className="px-6 py-4"
                    onSubmit={(event) => {
                        event.preventDefault();
                        if (!goal) return;
                        void onSave(goal, who.trim()).then((saved) => { if (saved) onClose(); });
                    }}
                >
                    <DialogDescription className="mb-3 text-[13px] text-muted">{t("goalsPage.waitingDialog.description")}</DialogDescription>
                    <input
                        autoFocus
                        value={who}
                        onChange={(event) => setWho(event.target.value)}
                        placeholder={t("goalsPage.waitingDialog.placeholder")}
                        aria-label={t("goalsPage.waitingDialog.placeholder")}
                        className="w-full rounded-md border border-border bg-background px-3 py-2 text-[13px] text-ink outline-none focus:border-border-emphasis"
                    />
                    <div className="mt-4 flex justify-end">
                        <Button size="sm" type="submit">{t("goalsPage.save")}</Button>
                    </div>
                </form>
            </DialogContent>
        </Dialog>
    );
}

function EditDialog({ goal, t, onClose, onSave }: {
    goal: GoalItemView | null;
    t: Translate;
    onClose: () => void;
    /** due is undefined when the user left the date alone, so a due the form
     *  does not show (a window, a condition) is kept. */
    onSave: (goal: GoalItemView, title: string, due: string | undefined) => Promise<boolean>;
}) {
    const [title, setTitle] = useState("");
    const [dueDate, setDueDate] = useState("");
    const [dueTouched, setDueTouched] = useState(false);
    useEffect(() => {
        setTitle(goal?.title ?? "");
        setDueDate(goal?.dueDate ?? "");
        setDueTouched(false);
    }, [goal?.id, goal?.title, goal?.dueDate]);
    const changeDue = (value: string) => {
        setDueDate(value);
        setDueTouched(true);
    };
    return (
        <Dialog open={!!goal} onOpenChange={(open) => { if (!open) onClose(); }}>
            <DialogContent className="w-[420px]">
                <DialogHeader>
                    <DialogTitle className="text-sm">{t("goalsPage.editDialog.title")}</DialogTitle>
                    <DialogCloseButton />
                </DialogHeader>
                <form
                    className="flex flex-col gap-3 px-6 py-4"
                    onSubmit={(event) => {
                        event.preventDefault();
                        if (!goal || !title.trim()) return;
                        void onSave(goal, title.trim(), dueTouched ? dueDate : undefined).then((saved) => { if (saved) onClose(); });
                    }}
                >
                    <DialogDescription className="sr-only">{t("goalsPage.editDialog.title")}</DialogDescription>
                    <label className="flex flex-col gap-1 text-[12px] text-muted">
                        {t("goalsPage.editDialog.goal")}
                        <input
                            autoFocus
                            value={title}
                            onChange={(event) => setTitle(event.target.value)}
                            className="rounded-md border border-border bg-background px-3 py-2 text-[13px] text-ink outline-none focus:border-border-emphasis"
                        />
                    </label>
                    <label className="flex flex-col gap-1 text-[12px] text-muted">
                        {t("goalsPage.editDialog.due")}
                        <input
                            type="date"
                            value={dueDate}
                            onChange={(event) => changeDue(event.target.value)}
                            className="rounded-md border border-border bg-background px-3 py-2 text-[13px] text-ink outline-none focus:border-border-emphasis"
                        />
                    </label>
                    {dueDate && (
                        <button type="button" className="self-start text-[12px] font-medium text-muted hover:text-ink" onClick={() => changeDue("")}>
                            {t("goalsPage.editDialog.noDate")}
                        </button>
                    )}
                    <div className="mt-1 flex justify-end">
                        <Button size="sm" type="submit" disabled={!title.trim()}>{t("goalsPage.save")}</Button>
                    </div>
                </form>
            </DialogContent>
        </Dialog>
    );
}
