import { useState, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { CheckCircle2, ChevronDown, ChevronRight, Circle, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";
import type { GoalItemView } from "@/lib/wails-shim-app";
import { GoalDialogs, GoalInlineResult, GoalMenu, goalStatus, type GoalsBoard, type Translate } from "./GoalsPage";

/** The Goals page's sentences, read from the same app keys the Goals page uses,
 *  so About you says the same things in the same words. */
export function useGoalsCopy(): { tr: Translate; language: string } {
    const { t, i18n } = useTranslation("app");
    return {
        tr: (key, options) => t(key as never, options as never) as unknown as string,
        language: i18n.resolvedLanguage ?? i18n.language,
    };
}

/**
 * The Goals group on About you, above New this week and the belief groups. It
 * shows the goals the phone tracks, the ones kept on this computer and the done
 * ones folded below, with the same moves as the Goals page. A row opens its
 * story in the page's detail beside the list.
 */
export function GoalsGroup({ board, now, open, onToggle, onCreateGoal, onOpenGoal, selectedId, groupRef }: {
    board: GoalsBoard;
    now: Date;
    /** False while the group is folded; the page keeps that in its collapsed set. */
    open: boolean;
    onToggle: () => void;
    /** Opens Chat with "I want to set a new goal" typed. Absent, Add a goal is not shown. */
    onCreateGoal?: () => void;
    /** Opens a goal"s story in the page"s detail. */
    onOpenGoal: (goal: GoalItemView) => void;
    /** The goal whose story is open; its row shows as selected. */
    selectedId: string | null;
    groupRef?: RefObject<HTMLElement>;
}) {
    const { t } = useTranslation("memory");
    const { tr, language } = useGoalsCopy();
    const [doneOpen, setDoneOpen] = useState(false);
    const { page, loading, failed, refresh, tracking, held, done } = board;
    const openCount = tracking.length + held.length;
    const nothingOpen = !!page && openCount === 0 && done.length === 0;
    const rowShared = { board, now, language, tr, selectedId, onOpen: onOpenGoal };

    return (
        <>
            <Collapsible asChild open={open} onOpenChange={onToggle}>
                <section ref={groupRef} aria-label={t("aboutYou.goals.title")} data-testid="goals-group" className="flex min-w-0 flex-col gap-1.5">
                    <div className="flex items-center justify-between gap-3">
                        <h2 className="m-0 min-w-0">
                            <CollapsibleTrigger className="inline-flex items-baseline gap-2 rounded-md pb-1 text-left text-base font-semibold text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus">
                                <span>{t("aboutYou.goals.title")}</span>
                                {openCount > 0 && <span className="text-[12.5px] font-normal text-muted">{openCount}</span>}
                                {open
                                    ? <ChevronDown className="h-3.5 w-3.5 self-center text-muted" />
                                    : <ChevronRight className="h-3.5 w-3.5 self-center text-muted" />}
                            </CollapsibleTrigger>
                        </h2>
                        {onCreateGoal && (
                            <button
                                type="button"
                                data-testid="goals-add"
                                onClick={onCreateGoal}
                                className="inline-flex shrink-0 items-center gap-1.5 rounded-md px-2 py-1 text-[12.5px] font-medium text-muted transition-colors hover:bg-surface-hover hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus"
                            >
                                <Plus className="h-3 w-3" />
                                {t("aboutYou.goals.add")}
                            </button>
                        )}
                    </div>
                    <CollapsibleContent className="flex flex-col gap-3">
                        {!page && loading && (
                            // Never read yet: static rows, never a spinner
                            // (snappy-and-first-run-1.0.md, section 2).
                            <div role="status" aria-busy="true" aria-label={t("aboutYou.goals.loading")} className="flex flex-col px-2.5">
                                {[0, 1].map((row) => (
                                    <div key={row} className="flex flex-col gap-2 py-2.5">
                                        <span className="h-3.5 w-2/5 rounded-md bg-surface-active motion-safe:animate-pulse" />
                                        <span className="h-3 w-3/5 rounded-md bg-surface-active motion-safe:animate-pulse" />
                                    </div>
                                ))}
                            </div>
                        )}
                        {failed && (
                            <div role="alert" className="flex items-center gap-3 rounded-xl border border-border bg-panel px-4 py-3.5 text-[13px] text-ink">
                                <span className="flex-1">{t("aboutYou.goals.loadFailed")}</span>
                                <Button size="sm" variant="secondary" onClick={refresh}>{t("aboutYou.goals.retry")}</Button>
                            </div>
                        )}
                        {nothingOpen && (
                            <p data-testid="goals-empty" className="m-0 px-2.5 py-1 text-[13px] text-muted">{t("aboutYou.goals.empty")}</p>
                        )}
                        {tracking.length > 0 && (
                            <ul className="flex flex-col gap-0.5">
                                {tracking.map((goal) => (
                                    <GoalRow key={goal.id} goal={goal} {...rowShared} />
                                ))}
                            </ul>
                        )}
                        {held.length > 0 && (
                            <div data-testid="goals-held" className="flex flex-col gap-0.5">
                                <p className="m-0 px-2.5 pb-0.5 text-[12px] text-muted">{t("aboutYou.goals.held", { count: held.length })}</p>
                                <ul className="flex flex-col gap-0.5">
                                    {held.map((goal) => (
                                        <GoalRow key={goal.id} goal={goal} {...rowShared} held />
                                    ))}
                                </ul>
                            </div>
                        )}
                        {done.length > 0 && (
                            <div data-testid="goals-done" className="flex flex-col gap-0.5">
                                <button
                                    type="button"
                                    aria-expanded={doneOpen}
                                    onClick={() => setDoneOpen((value) => !value)}
                                    className="inline-flex items-center gap-1.5 self-start rounded-md px-2.5 py-1 text-[12.5px] text-muted transition-colors hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus"
                                >
                                    {t("aboutYou.goals.done", { count: done.length })}
                                    {doneOpen
                                        ? <ChevronDown className="h-3.5 w-3.5" />
                                        : <ChevronRight className="h-3.5 w-3.5" />}
                                </button>
                                {doneOpen && (
                                    <ul className="flex flex-col gap-0.5">
                                        {done.map((goal) => (
                                            <GoalRow key={goal.id} goal={goal} {...rowShared} />
                                        ))}
                                    </ul>
                                )}
                            </div>
                        )}
                    </CollapsibleContent>
                </section>
            </Collapsible>
            <GoalDialogs board={board} t={tr} />
        </>
    );
}

interface GoalRowProps {
    goal: GoalItemView;
    board: GoalsBoard;
    now: Date;
    language: string;
    tr: Translate;
    selectedId: string | null;
    onOpen: (goal: GoalItemView) => void;
    held?: boolean;
}

/** One goal: its title and status line, a chip for its state, the ··· moves and
 *  the circle that marks it done (or reopens it). */
function GoalRow({ goal, board, now, language, tr, selectedId, onOpen, held = false }: GoalRowProps) {
    const { t } = useTranslation("memory");
    const done = goal.state === "resolved";
    const waiting = goal.state === "waiting";
    const selected = selectedId === goal.id;
    const status = held ? tr("goalsPage.heldReason") : goalStatus(goal, now, language, tr);
    const pending = board.pendingMoves[goal.id];
    return (
        <li
            data-testid="goal-row"
            data-goal-id={goal.id}
            className={cn(
                "group flex items-center gap-2 rounded-lg py-1.5 pl-2.5 pr-1.5 transition-colors",
                selected ? "bg-surface-active" : "hover:bg-surface-hover",
            )}
        >
            {pending ? <div className="min-w-0 flex-1"><span className="block text-[13px] text-ink">{goal.title}</span><GoalInlineResult pending={pending} undo={() => void board.undoMove(goal.id)} t={tr} /></div> : <>
            <button
                type="button"
                aria-pressed={selected}
                onClick={() => onOpen(goal)}
                className="min-w-0 flex-1 rounded-md py-0.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus"
            >
                <span className={cn("block break-words text-[13px] leading-[1.45]", done ? "text-muted" : "text-ink")}>{goal.title}</span>
                <span className={cn("mt-0.5 block text-[12px] leading-snug", goal.slipping && !done ? "text-accent" : "text-muted")}>{status}</span>
            </button>
            {!done && (waiting
                ? <span className="shrink-0 rounded-full bg-copper/10 px-2 py-0.5 text-[11px] font-semibold text-copper">{t("aboutYou.goals.waiting")}</span>
                : <span className="shrink-0 rounded-full bg-surface-active px-2 py-0.5 text-[11px] text-muted">{t("aboutYou.goals.tracking")}</span>)}
            <GoalMenu goal={goal} t={tr} actions={board.actions} />
            <button
                type="button"
                aria-label={tr(done ? "goalsPage.reopenLabel" : "goalsPage.checkLabel", { title: goal.title })}
                onClick={() => board.actions.toggle(goal)}
                className="flex size-6 shrink-0 items-center justify-center rounded-full text-muted transition-colors hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus"
            >
                {done ? <CheckCircle2 className="size-4" /> : <Circle className="size-4" />}
            </button>
            </>}
        </li>
    );
}
