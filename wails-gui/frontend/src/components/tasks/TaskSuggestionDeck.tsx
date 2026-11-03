import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { measureTasks, recordTasksAction, useTasksSectionSeen } from "@/lib/tasks-telemetry";
import { FailureLine } from "./FailureLine";
import { MoreHorizontal } from "lucide-react";
import { DecideTaskSuggestion, FindMoreTaskSuggestions, ListTaskSuggestions, type TaskSuggestionView } from "@/lib/wails-shim-app";
import { onPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { TaskSectionHeader } from "./TaskSectionHeader";
import { beginTaskStart, reconcilePendingTaskStarts, taskOfferStarted, usePendingTaskStarts } from "./pending-task-starts";
import { computerClock, computerHourOptions } from "@/lib/brief-time";

const SOURCE_EVENTS = [
    "knowledge:updated", "activity:activities-updated", "activity:threads-updated", "activity:timeline-groups-updated",
    "activity:session-saved", "activity:status", "activity:summary-ready", "activity:moments-ready", "activity:summary-error",
    "meeting-notes:saved", "insight:saved", "cron:state", "app:ready", "ai:config-changed",
    "topics:finds", "topics:updated", "initiative:updated",
];

/** How long Not now waits for Undo before it tells the computer. */
export const NOT_NOW_UNDO_MS = 5000;
// Offers whose Not now failed to save, even after leaving Tasks (T09).
const notSavedOffers = new Set<string>();

type TaskSuggestionDeckProps = {
    /** The first day of the first week: no offers yet, so the section says when
     *  they start instead of offering Find more. */
    firstDay?: boolean;
    extraCards?: ReactNode[];
};

/**
 * Tasks › Up next: the ideas Isoform has for you, two columns of cards with
 * Do it / Not now (sidebar handoff 1.0.0, section 4). It always shows, since
 * Find more lives in its heading. Not now hides the card at once and waits for
 * Undo before it is sent; it never asks for Face ID (hub section 13). Every
 * card is withdrawn the moment a privacy setting changes.
 */
export function TaskSuggestionDeck({ firstDay = false, extraCards = [] }: TaskSuggestionDeckProps) {
    const { t, i18n } = useTranslation("app");
    const pendingStarts = usePendingTaskStarts();
    const [offers, setOffers] = useState<TaskSuggestionView[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState(false);
    const [leaving, setLeaving] = useState<string | null>(null);
    const [busy, setBusy] = useState(false);
    const [hidden, setHidden] = useState<Set<string>>(new Set());
    const [finding, setFinding] = useState(false);
    const [findLine, setFindLine] = useState<string | null>(null);
    const [startFailed, setStartFailed] = useState<string | null>(null);
    const [away, setAway] = useState<ReadonlySet<string>>(new Set());
    const [, setNotSavedCount] = useState(0);
    const locked = useRef(false);
    const request = useRef(0);
    const loadRequest = useRef(0);
    const offersRef = useRef<TaskSuggestionView[]>([]);
    const pendingId = useRef<string | null>(null);
    const notNowTimers = useRef(new Map<string, number>());
    const mounted = useRef(true);
    const replaceOffers = useCallback((rows: TaskSuggestionView[]) => {
        offersRef.current = rows;
        setOffers(rows);
    }, []);
    const reload = useCallback(async (background = false) => {
        const id = ++loadRequest.current;
        const generation = request.current;
        if (!background) setLoading(true);
        try {
            const rows = await ListTaskSuggestions();
            if (!mounted.current || id !== loadRequest.current || generation !== request.current) return;
            const next = rows ?? [];
            reconcilePendingTaskStarts(next);
            const pending = offersRef.current.find((row) => row.id === pendingId.current);
            if (pending && !next.some((row) => row.id === pending.id && row.version === pending.version)) {
                request.current++;
                setLeaving(null);
            }
            replaceOffers(next);
            setError(false);
        } catch {
            if (mounted.current && id === loadRequest.current && generation === request.current) setError(true);
        } finally {
            if (mounted.current && id === loadRequest.current) setLoading(false);
        }
    }, [replaceOffers]);
    useEffect(() => {
        mounted.current = true;
        const timers = notNowTimers.current;
        void reload();
        return () => {
            mounted.current = false;
            request.current++;
            loadRequest.current++;
            // Leaving the page sends every Not now that was still waiting for Undo.
            timers.forEach((timer, id) => { window.clearTimeout(timer); void DecideTaskSuggestion(id, "not_now").catch(() => { notSavedOffers.add(id); }); });
            timers.clear();
        };
    }, [reload]);
    useEffect(() => {
        let timer: number | undefined;
        const cancelReload = () => window.clearTimeout(timer);
        const refresh = () => {
            cancelReload();
            timer = window.setTimeout(() => void reload(true), 500);
        };
        const withdraw = () => {
            cancelReload();
            request.current++;
            replaceOffers([]);
            setLeaving(null);
            setError(false);
            setFindLine(null);
            void reload();
        };
        const cleanups = [
            onPagePrivacyWithdrawal(withdraw),
            EventsOn("topics:withdrawn", withdraw),
            ...SOURCE_EVENTS.map((event) => EventsOn(event, refresh)),
        ];
        return () => { cancelReload(); cleanups.forEach((cleanup) => cleanup()); };
    }, [reload, replaceOffers]);
    const choose = useCallback(async (offer: TaskSuggestionView, decision: "do_it" | "dismiss", via: "T07" | "T31" = "T07") => {
        if (locked.current || loading) return;
        if (decision === "do_it") {
            setStartFailed(null);
            notSavedOffers.delete(offer.id);
            const started = performance.now();
            beginTaskStart(offer, () => { recordTasksAction(via, "do", "failed", started); if (mounted.current) { setStartFailed(offer.id); void reload(true); } }, () => recordTasksAction(via, "do", "done", started));
            return;
        }
        const generation = request.current;
        const isCurrent = () => mounted.current && generation === request.current;
        locked.current = true;
        pendingId.current = offer.id;
        setBusy(true);
        setError(false);
        try {
            await measureTasks("T10", "do", "done", () => DecideTaskSuggestion(offer.id, decision));
            if (!isCurrent()) return;
            setLeaving(offer.id);
            await new Promise((resolve) => window.setTimeout(resolve, window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ? 150 : 400));
            if (!isCurrent()) return;
            if (isCurrent()) {
                loadRequest.current++;
                replaceOffers(offersRef.current.filter((row) => row.id !== offer.id));
            }
        } catch {
            if (isCurrent()) {
                setError(true);
            }
        } finally {
            locked.current = false;
            pendingId.current = null;
            if (mounted.current) { setLeaving(null); setBusy(false); }
        }
    }, [loading, reload, replaceOffers, t]);
    const unaway = (id: string) => setAway((old) => { const next = new Set(old); next.delete(id); return next; });
    const unhide = (id: string) => setHidden((old) => { const next = new Set(old); next.delete(id); return next; });
    const notNow = useCallback((offer: TaskSuggestionView, via: "T08" | "T31" = "T08") => {
        recordTasksAction(via, "do", "done");
        notSavedOffers.delete(offer.id);
        setHidden((old) => new Set(old).add(offer.id));
        setAway((old) => new Set(old).add(offer.id));
        const timer = window.setTimeout(() => {
            notNowTimers.current.delete(offer.id);
            if (mounted.current) unaway(offer.id);
            void DecideTaskSuggestion(offer.id, "not_now").catch(() => {
                recordTasksAction("T09", "do", "failed");
                notSavedOffers.add(offer.id);
                if (mounted.current) { unhide(offer.id); setNotSavedCount((count) => count + 1); }
            });
        }, NOT_NOW_UNDO_MS);
        notNowTimers.current.set(offer.id, timer);
    }, []);
    const undoNotNow = (id: string) => {
        recordTasksAction("T08", "do", "undone");
        window.clearTimeout(notNowTimers.current.get(id));
        notNowTimers.current.delete(id);
        unhide(id);
        unaway(id);
    };
    const findMore = useCallback(async () => {
        if (finding) return;
        const generation = request.current;
        const before = new Set(offersRef.current.map((row) => row.id));
        setFinding(true);
        setFindLine(null);
        try {
            const result = await measureTasks("T11", "ask", "done", () => FindMoreTaskSuggestions());
            if (!mounted.current || generation !== request.current) return;
            const next = result?.offers ?? [];
            reconcilePendingTaskStarts(next);
            loadRequest.current++;
            replaceOffers(next);
            const nextAt = result?.nextAllowedAt ? new Date(result.nextAllowedAt) : null;
            if (nextAt && !Number.isNaN(nextAt.getTime())) {
                setFindLine(t("tasks.findMoreAfter", { time: computerClock(nextAt, i18n.language) }));
            } else if (next.every((row) => before.has(row.id))) {
                setFindLine(t("tasks.findMoreNothing"));
            }
        } catch {
            if (mounted.current && generation === request.current) setFindLine(t("tasks.findMoreFailed"));
        } finally {
            if (mounted.current) setFinding(false);
        }
    }, [finding, i18n.language, replaceOffers, t]);
    const visible = offers.filter((offer) => !hidden.has(offer.id) && !taskOfferStarted(offer) && !pendingStarts.some((item) => item.offer.id === offer.id) && offer.evidence?.text && offer.evidence.source && offer.evidence.at);
    // The keys the old deck had: Right arrow is Do it and Left arrow is Not
    // now, on the first offer, when nothing else has the keyboard.
    const first = visible[0];
    useEffect(() => {
        const onKey = (event: KeyboardEvent) => {
            if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
            if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
            const target = event.target instanceof Element ? event.target : document.activeElement;
            if (target?.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="dialog"], [role="menu"], [role="listbox"]')) return;
            if (document.querySelector('[role="dialog"][data-state="open"], [role="menu"][data-state="open"], [role="listbox"][data-state="open"]')) return;
            if (!first || loading || locked.current) return;
            event.preventDefault();
            if (event.key === "ArrowRight") void choose(first, "do_it", "T31");
            else notNow(first, "T31");
        };
        document.addEventListener("keydown", onKey);
        return () => document.removeEventListener("keydown", onKey);
    }, [choose, notNow, first, loading]);
    // A Not now waits in place for 5 s with Undo, never as a toast.
    const cards = offers.filter((offer) => visible.includes(offer) || (away.has(offer.id) && hidden.has(offer.id)));
    const empty = cards.length === 0 && extraCards.length === 0;
    const showFindMore = !(firstDay && empty);
    useTasksSectionSeen("up_next", cards.length + extraCards.length, !loading);
    const firstDayShown = firstDay && empty && !loading;
    useEffect(() => { if (firstDayShown) recordTasksAction("T12", "open", "opened"); }, [firstDayShown]);
    const evidence = (offer: TaskSuggestionView) => offer.evidence?.textLocalized?.[i18n.language] || offer.evidence?.textLocalized?.[i18n.language.split("-")[0]] || offer.evidence?.text;
    const sourceLine = (offer: TaskSuggestionView) => {
        const fact = offer.evidence!;
        const sourceKeys: Record<string, "mail" | "calendar" | "claudeCode" | "codex" | "call" | "screen"> = { mail: "mail", calendar: "calendar", claude_code: "claudeCode", codex: "codex", call: "call", screen: "screen" };
        const source = t(`tasks.evidenceCard.${sourceKeys[fact.source] ?? "screen"}`);
        const date = new Date(fact.at);
        const when = Number.isNaN(date.getTime()) ? fact.at : date.toLocaleString(i18n.language, { ...(new Date().toDateString() === date.toDateString() ? {} : { weekday: "short" }), hour: "numeric", minute: "2-digit", ...computerHourOptions() });
        return t("tasks.evidenceCard.source", { source, when });
    };
    return (
        <section aria-label={t("tasks.upNext")} aria-description={visible.length > 0 ? t("tasks.deckKeys") : undefined} aria-busy={busy || undefined} data-tasks-section="up-next">
            <TaskSectionHeader
                title={t("tasks.upNext")}
                action={showFindMore ? <Button variant="quiet" size="sm" className="text-muted" disabled={finding || busy || loading} onClick={() => void findMore()}>{t("tasks.findMore")}</Button> : undefined}
            />
            {error && <FailureLine tone="copper" className="mb-3" text={t("tasks.suggestionFailed")} />}
            {!empty ? (
                <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                    {cards.map((offer) => away.has(offer.id) ? (
                        <p key={offer.id} data-task-offer-away={offer.id} role="status" className="flex items-center gap-1 rounded-xl border border-border px-4 py-3 text-[13px] text-muted">{t("tasks.notNowAway", { title: offer.title })} ·<Button variant="quiet" size="sm" onClick={() => undoNotNow(offer.id)}>{t("tasks.undo")}</Button></p>
                    ) : (
                        <article key={offer.id} data-task-offer={offer.id} data-leaving={leaving === offer.id || undefined} className={`flex flex-col rounded-xl border border-border bg-panel px-4 py-4 transition-opacity duration-[400ms] motion-reduce:duration-150 ${leaving === offer.id ? "opacity-0" : "opacity-100"}`}>
                            {offer.deckKind && <p className="mb-2 text-xs font-semibold uppercase text-muted">{offer.deckKind === "reply" ? t("tasks.evidenceCard.reply") : offer.deckKind === "your_day" ? t("tasks.evidenceCard.yourDay") : offer.deckKind === "claude_code" ? t("tasks.evidenceCard.claudeCode") : null}</p>}
                            <div className="flex items-start gap-2">
                                <h3 className="min-w-0 flex-1 text-[14px] font-semibold leading-snug text-ink">{offer.title}</h3>
                                <DropdownMenu>
                                    <DropdownMenuTrigger asChild><Button variant="quiet" size="icon" className="-mr-2 -mt-1 size-7" disabled={busy} aria-label={t("tasks.more")}><MoreHorizontal className="size-4" /></Button></DropdownMenuTrigger>
                                    <DropdownMenuContent align="end"><DropdownMenuItem onSelect={() => void choose(offer, "dismiss")}>{t("tasks.notForMe")}</DropdownMenuItem></DropdownMenuContent>
                                </DropdownMenu>
                            </div>
                            <p className="mt-2 text-[13px] leading-relaxed text-muted">{evidence(offer)}</p>
                            <p className="mt-2 text-xs text-muted">{sourceLine(offer)}</p>
                            {notSavedOffers.has(offer.id) && <p role="status" className="mt-2 text-[13px] text-muted">{t("tasks.notNowNotSaved")}</p>}
                            {startFailed === offer.id && <FailureLine tone="copper" className="mt-2" text={t("taskThread.startFailed")} onRetry={() => void choose(offer, "do_it")} />}
                            <div className="mt-auto flex gap-2 pt-4">
                                <Button size="sm" disabled={busy || loading} onClick={() => void choose(offer, "do_it")}>{t("tasks.doIt")}</Button>
                                <Button variant="secondary" size="sm" disabled={busy || loading} onClick={() => notNow(offer)}>{t("tasks.notNow")}</Button>
                            </div>
                        </article>
                    ))}
                    {extraCards}
                </div>
            ) : loading ? (
                // Never read yet: a static card in the shape of an idea,
                // never "Loading…" (snappy-and-first-run-1.0.md, section 2).
                <div data-testid="task-deck-placeholder" aria-hidden className="grid grid-cols-1 gap-3 md:grid-cols-2">
                    <div className="rounded-xl border border-border bg-panel px-4 py-4">
                        <span className="block h-3.5 w-3/5 rounded-md bg-surface-active" />
                        <div className="mt-3 flex flex-col gap-2"><span className="h-3 w-full rounded-md bg-surface-active" /><span className="h-3 w-4/5 rounded-md bg-surface-active" /></div>
                        <div className="mt-4 flex gap-2"><span className="h-7 w-14 rounded-lg bg-surface-active" /><span className="h-7 w-20 rounded-lg bg-surface-active" /></div>
                    </div>
                </div>
            ) : (
                <p data-testid="up-next-empty" className="text-[13px] text-muted">{firstDay ? t("tasks.upNextFirstDay") : t("tasks.upNextEmpty")}</p>
            )}
            {findLine && <p role="status" className="mt-2 text-[12px] text-muted">{findLine}</p>}
        </section>
    );
}
