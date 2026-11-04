import { useRef, useState, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { CheckCircle2, ChevronDown, ChevronRight, Circle, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";
import { CreateGoal, type GoalItemView } from "@/lib/wails-shim-app";
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
export function GoalsGroup({ board, now, open, onToggle, adding, onAddingChange, onOpenGoal, selectedId, groupRef }: {
    board: GoalsBoard;
    now: Date;
    /** False while the group is folded; the page keeps that in its collapsed set. */
    open: boolean;
    onToggle: () => void;
    /** Add a goal's field is open at the top of the group (the page's More menu opens it while there are no goals). */
    adding: boolean;
    onAddingChange: (adding: boolean) => void;
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
    // Goals added here this visit: shown under Tracking at once, and their
    // line says so until the page reads them back.
    const [added, setAdded] = useState<{ key: string; goal: GoalItemView | null; title: string }[]>([]);
    const [draft, setDraft] = useState<{ text: string; key: string; failed: boolean }>({ text: "", key: newGoalKey(), failed: false });
    const addedIds = new Set(added.map((item) => item.goal?.id).filter(Boolean));
    const provisional = added.filter((item) => !item.goal || !tracking.some((goal) => goal.id === item.goal!.id));
    const nothing = !!page && openCount === 0 && done.length === 0 && added.length === 0;
    const rowShared = { board, now, language, tr, selectedId, onOpen: onOpenGoal, addedIds };

    const submit = async (text: string, key: string) => {
        const title = text.trim();
        if (!title) return;
        onAddingChange(false);
        setAdded((current) => [...current.filter((item) => item.key !== key), { key, goal: null, title }]);
        try {
            const result = await CreateGoal({ text: title, key });
            const goal = { ...(provisionalGoal(title)), id: result.id, eventId: result.eventId, title: result.title || title };
            setAdded((current) => current.map((item) => item.key === key ? { ...item, goal } : item));
            setDraft({ text: "", key: newGoalKey(), failed: false });
            refresh();
        } catch {
            setAdded((current) => current.filter((item) => item.key !== key));
            setDraft({ text, key, failed: true });
            onAddingChange(true);
        }
    };

    // No goals, no Goals section: Add a goal lives in the page's More menu then.
    if (nothing && !adding && !failed) return <GoalDialogs board={board} t={tr} />;

    return (
        <>
            <Collapsible asChild open={open} onOpenChange={onToggle}>
                <section ref={groupRef} aria-label={t("aboutYou.goals.title")} data-testid="goals-group" className="flex min-w-0 flex-col gap-1.5">
                    <div className="flex items-center justify-between gap-3">
                        <h2 className="m-0 min-w-0">
                            <CollapsibleTrigger className="inline-flex items-baseline gap-2 rounded-md pb-1 text-left text-base font-semibold text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus">
                                <span>{t("aboutYou.goals.title")}</span>
                                {open
                                    ? <ChevronDown className="h-3.5 w-3.5 self-center text-muted" />
                                    : <ChevronRight className="h-3.5 w-3.5 self-center text-muted" />}
                            </CollapsibleTrigger>
                        </h2>
                        {!nothing && !adding && (
                            <button
                                type="button"
                                data-testid="goals-add"
                                onClick={() => onAddingChange(true)}
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
                        {adding && (
                            <AddGoalField
                                initial={draft.text}
                                failed={draft.failed}
                                onAdd={(text) => void submit(text, draft.failed ? draft.key : newGoalKey())}
                                onCancel={() => {
                                    setDraft({ text: "", key: newGoalKey(), failed: false });
                                    onAddingChange(false);
                                }}
                            />
                        )}
                        {failed && (
                            <div role="alert" className="flex items-center gap-3 rounded-xl border border-border bg-panel px-4 py-3.5 text-[13px] text-copper">
                                <span className="flex-1">{t("aboutYou.goals.loadFailed")}</span>
                                <Button size="sm" variant="secondary" onClick={refresh}>{t("aboutYou.goals.retry")}</Button>
                            </div>
                        )}
                        {(tracking.length > 0 || provisional.length > 0) && (
                            <ul className="flex flex-col gap-0.5">
                                {provisional.map((item) => (
                                    <li key={item.key} data-testid="goal-row-added" className="flex flex-col rounded-lg py-1.5 pl-2.5 pr-1.5">
                                        <span className="block break-words text-[13px] leading-[1.45] text-ink">{item.title}</span>
                                        <span className="mt-0.5 block text-[12px] leading-snug text-muted">{t("aboutYou.goals.addedToday")}</span>
                                    </li>
                                ))}
                                {tracking.map((goal) => (
                                    <GoalRow key={goal.id} goal={goal} {...rowShared} />
                                ))}
                            </ul>
                        )}
                        {held.length > 0 && (
                            <div data-testid="goals-held" className="flex flex-col gap-0.5">
                                <p className="m-0 px-2.5 pb-0.5 text-[12px] text-muted">{t("aboutYou.goals.held")}</p>
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
                                    {t("aboutYou.goals.done")}
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

function newGoalKey(): string {
    return typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function provisionalGoal(title: string): GoalItemView {
    return { id: "", eventId: "", title, state: "active", group: "tracking", origin: "chat" } as unknown as GoalItemView;
}

/** Add a goal, in place at the top of Goals. A failed save keeps the words. */
function AddGoalField({ initial, failed, onAdd, onCancel }: { initial: string; failed: boolean; onAdd: (text: string) => void; onCancel: () => void }) {
    const { t } = useTranslation("memory");
    const [text, setText] = useState(initial);
    const inputRef = useRef<HTMLInputElement>(null);
    return (
        <form
            data-testid="goals-add-field"
            className="flex flex-col gap-2 px-2.5 py-1"
            onSubmit={(event) => {
                event.preventDefault();
                onAdd(text);
            }}
        >
            <label className="flex flex-col gap-1.5 text-[13px] text-ink">
                {t("aboutYou.goals.addField.label")}
                <input
                    ref={inputRef}
                    autoFocus
                    value={text}
                    onChange={(event) => setText(event.target.value)}
                    onKeyDown={(event) => {
                        if (event.key === "Escape") {
                            event.stopPropagation();
                            onCancel();
                        }
                    }}
                    placeholder={t("aboutYou.goals.addField.placeholder")}
                    className="h-8 rounded-md border border-border bg-background px-2.5 text-[13px] text-ink placeholder:text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus"
                />
            </label>
            {failed && (
                <p role="alert" className="m-0 flex flex-wrap items-center gap-2 text-[12.5px] text-copper">
                    <span>{t("aboutYou.goals.addField.failed")}</span>
                    <button type="button" className="text-accent hover:underline" onClick={() => onAdd(text)}>{t("aboutYou.retry")}</button>
                </p>
            )}
            <div className="flex gap-2">
                <Button size="sm" type="submit" disabled={!text.trim()}>{t("aboutYou.goals.addField.add")}</Button>
                <Button size="sm" type="button" variant="secondary" onClick={onCancel}>{t("aboutYou.goals.addField.cancel")}</Button>
            </div>
        </form>
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
    addedIds?: Set<string | undefined>;
}

/** One goal: its title and status line, a chip for its state, the ··· moves and
 *  the circle that marks it done (or reopens it). */
function GoalRow({ goal, board, now, language, tr, selectedId, onOpen, held = false, addedIds }: GoalRowProps) {
    const { t } = useTranslation("memory");
    const done = goal.state === "resolved";
    const waiting = goal.state === "waiting";
    const selected = selectedId === goal.id;
    const status = held ? tr("goalsPage.heldReason") : addedIds?.has(goal.id) ? t("aboutYou.goals.addedToday") : goalStatus(goal, now, language, tr);
    const pending = board.pendingMoves[goal.id];
    const retryMove = board.failedMoves[goal.id];
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
                {retryMove && <span role="alert" className="mt-0.5 block text-[12px] leading-snug text-copper">{t("aboutYou.goals.moveFailed")}</span>}
            </button>
            {retryMove && (
                <button type="button" className="shrink-0 text-[12px] text-accent hover:underline" onClick={retryMove}>{t("aboutYou.retry")}</button>
            )}
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
