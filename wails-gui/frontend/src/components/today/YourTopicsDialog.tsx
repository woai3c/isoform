import { measureToday, recordTodayAction } from "@/lib/today-telemetry";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2, Pencil, Pin, PinOff, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import {
    GetUserTopics,
    PinTopicToSidebar,
    SetTopicFindsEnabled,
    UpdateUserTopic,
    type UserTopic,
    type UserTopicAction,
    type UserTopicsView,
} from "@/lib/wails-shim-app";

import { PageEventsOn } from "@/lib/page-queries";
import { SIDEBAR_TOPICS_CHANGED } from "./topics";

type Translate = (key: string, options?: Record<string, unknown>) => string;

const FIELD_CLASS =
    "min-w-0 flex-1 rounded-md border border-border bg-panel px-2 py-1 text-[13px] text-ink focus:outline-none focus:ring-1 focus:ring-accent";
const ICON_BUTTON =
    "inline-flex size-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-ink/[0.06] hover:text-ink disabled:opacity-50";

/**
 * "Your topics": what Isoform found the user looking at, with pin, rename,
 * remove and bring back, and the switch for "New on your topics". The copy
 * says what leaves the Mac: only the topic names, for the search.
 */
export function YourTopicsDialog({
    open,
    onOpenChange,
    onChanged,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    /** Called after any change, so Today re-reads its finds. */
    onChanged?: () => void;
}) {
    const { t } = useTranslation("today");
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const [view, setView] = useState<UserTopicsView | null>(null);
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    const [loadFailed, setLoadFailed] = useState(false);
    const [attempt, setAttempt] = useState(0);
    const [editing, setEditing] = useState<string | null>(null);
    const [draft, setDraft] = useState("");

    useEffect(() => {
        if (!open) return;
        let live = true;
        setFailed(false);
        setLoadFailed(false);
        GetUserTopics()
            .then((next) => {
                if (live) setView(next);
            })
            .catch(() => {
                if (live) setLoadFailed(true);
            });
        return () => {
            live = false;
        };
    }, [open, attempt]);

    useEffect(() => {
        if (!open) return;
        const refresh = () => setAttempt((value) => value + 1);
        const off = PageEventsOn("topics:updated", refresh);
        window.addEventListener(SIDEBAR_TOPICS_CHANGED, refresh);
        return () => { off(); window.removeEventListener(SIDEBAR_TOPICS_CHANGED, refresh); };
    }, [open]);

    const run = useCallback(
        async (call: () => Promise<UserTopicsView>) => {
            setBusy(true);
            setFailed(false);
            try {
                setView(await call());
                onChanged?.();
                return true;
            } catch {
                setFailed(true);
                return false;
            } finally {
                setBusy(false);
            }
        },
        [onChanged],
    );

    const change = async (topic: UserTopic, action: UserTopicAction, label = "") => {
        const started = performance.now();
        const id = action === "pin" || action === "unpin" ? "T56" : action === "rename" ? "T57" : "T58";
        const ok = await run(async () => {
            if (action === "pin" || action === "unpin") {
                await PinTopicToSidebar(topic.id, action === "pin");
                return GetUserTopics();
            }
            return UpdateUserTopic({ id: topic.id, action, label });
        });
        recordTodayAction(id, "do", ok ? (action === "restore" || action === "unpin" ? "undone" : "done") : "failed", started);
        return ok;
    };

    const [renameError, setRenameError] = useState<string | null>(null);
    const saveRename = async (topic: UserTopic) => {
        const problem = renameProblem(draft);
        setRenameError(problem);
        if (problem) return;
        if (await change(topic, "rename", draft)) setEditing(null);
    };

    // A topic Isoform cannot say why about stays out of sight until it can;
    // it still counts for merging and stays in storage. The phrase fallback
    // (before Isoform has named any interests) always shows.
    const shownTopics = view && view.interests ? view.topics.filter((topic) => topic.why) : (view?.topics ?? []);

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="w-[560px]" data-testid="your-topics">
                <DialogHeader>
                    <DialogTitle>{tr("topics.title")}</DialogTitle>
                    <DialogCloseButton />
                </DialogHeader>
                <div className="flex min-h-0 flex-col gap-4 overflow-y-auto px-6 py-5">
                    <DialogDescription className="text-[13px] leading-relaxed">{tr("topics.description")}</DialogDescription>

                    <label className="flex items-start justify-between gap-4 rounded-lg bg-ink/[0.03] px-3.5 py-3">
                        <span>
                            <span className="block text-[13px] font-medium text-ink">{tr("topics.switch")}</span>
                            <span className="mt-0.5 block text-[12px] text-muted">{tr("topics.switchHint")}</span>
                        </span>
                        <Switch
                            aria-label={tr("topics.switch")}
                            checked={view?.enabled ?? false}
                            disabled={!view || busy}
                            onCheckedChange={(checked) => { const started = performance.now(); void run(() => SetTopicFindsEnabled(checked)).then((ok) => recordTodayAction("T54", "do", ok ? "done" : "failed", started)); }}
                        />
                    </label>

                    {!view ? (
                        loadFailed ? (
                            <p role="alert" className="flex items-center gap-3 text-[13px] text-muted">
                                {tr("topics.loadFailed")}
                                <button
                                    type="button"
                                    className="text-[12px] font-medium text-muted transition-colors hover:text-ink"
                                    onClick={() => measureToday("T55", "do", "done", () => setAttempt((n) => n + 1))}
                                >
                                    {tr("topics.retry")}
                                </button>
                            </p>
                        ) : (
                            <p className="flex items-center gap-2 text-[13px] text-muted">
                                <Loader2 className="size-3.5 animate-spin" />
                                {tr("topics.loading")}
                            </p>
                        )
                    ) : shownTopics.length === 0 ? (
                        <p className="text-[13px] text-muted">{tr("topics.empty")}</p>
                    ) : (
                        <ul className="divide-y divide-border rounded-xl border border-border">
                            {shownTopics.map((topic) => (
                                <li key={topic.id} className="flex items-center gap-3 px-3.5 py-2.5" data-testid="topic-row">
                                    {editing === topic.id ? (
                                        <form
                                            className="flex flex-1 flex-wrap items-center gap-2"
                                            onSubmit={(event) => {
                                                event.preventDefault();
                                                void saveRename(topic);
                                            }}
                                        >
                                            <input
                                                aria-label={tr("topics.renameLabel")}
                                                className={FIELD_CLASS}
                                                value={draft}
                                                maxLength={48}
                                                autoFocus
                                                onChange={(event) => setDraft(event.target.value)}
                                            />
                                            <Button size="sm" type="submit" disabled={busy || !draft.trim()}>
                                                {tr("topics.save")}
                                            </Button>
                                            <Button
                                                size="sm"
                                                variant="ghost"
                                                type="button"
                                                onClick={() => measureToday("T57", "do", "stopped", () => {
                                                    setEditing(null);
                                                    setRenameError(null);
                                                })}
                                            >
                                                {tr("topics.cancel")}
                                            </Button>
                                            {renameError && (
                                                <span role="alert" className="basis-full text-[12px] text-muted">
                                                    {tr(renameError)}
                                                </span>
                                            )}
                                        </form>
                                    ) : (
                                        <>
                                            <TopicLabel topic={topic} tr={tr} />
                                            <button
                                                type="button"
                                                className={ICON_BUTTON}
                                                disabled={busy}
                                                aria-label={topic.pinned ? tr("topics.unpin") : tr("topics.pin")}
                                                title={topic.pinned ? tr("topics.unpin") : tr("topics.pin")}
                                                onClick={() => void change(topic, topic.pinned ? "unpin" : "pin")}
                                            >
                                                {topic.pinned ? <PinOff className="size-3.5" /> : <Pin className="size-3.5" />}
                                            </button>
                                            <button
                                                type="button"
                                                className={ICON_BUTTON}
                                                disabled={busy}
                                                aria-label={tr("topics.rename")}
                                                title={tr("topics.rename")}
                                                onClick={() => measureToday("T57", "open", "opened", () => {
                                                    setDraft(topic.label);
                                                    setEditing(topic.id);
                                                })}
                                            >
                                                <Pencil className="size-3.5" />
                                            </button>
                                            <button
                                                type="button"
                                                className={ICON_BUTTON}
                                                disabled={busy}
                                                aria-label={tr("topics.remove")}
                                                title={tr("topics.remove")}
                                                onClick={() => void change(topic, "remove")}
                                            >
                                                <X className="size-3.5" />
                                            </button>
                                        </>
                                    )}
                                </li>
                            ))}
                        </ul>
                    )}

                    {view && view.removed.length > 0 && (
                        <div>
                            <div className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted/70">
                                {tr("topics.removedTitle")}
                            </div>
                            <ul className="flex flex-col gap-1">
                                {view.removed.map((topic) => (
                                    <li key={topic.id} className="flex items-center justify-between gap-3 text-[13px] text-muted">
                                        <span className="truncate">{topic.label}</span>
                                        <button
                                            type="button"
                                            className="shrink-0 text-[12px] font-medium text-muted transition-colors hover:text-ink"
                                            disabled={busy}
                                            onClick={() => void change(topic, "restore")}
                                        >
                                            {tr("topics.restore")}
                                        </button>
                                    </li>
                                ))}
                            </ul>
                        </div>
                    )}

                    {failed && (
                        <p role="alert" className="text-[12px] text-muted">
                            {tr("topics.failed")}
                        </p>
                    )}
                </div>
            </DialogContent>
        </Dialog>
    );
}

/** The same checks the Mac makes before it keeps a name (a few words, no
 *  link, path or address), as the key of the message to show. */
export function renameProblem(raw: string): string | null {
    const label = raw.trim().split(/\s+/).filter(Boolean);
    const text = label.join(" ");
    if (text.length > 48 || label.length > 6) return "topics.renameTooLong";
    if (text.includes("://") || /[\\/@]/.test(text)) return "topics.renameNoLinks";
    return null;
}

const DAY_MS = 86_400_000;

/** Says when a topic was last seen, relative to today: "today", "yesterday",
 *  "2 days ago". */
export function describeTopicTime(iso: string, now: Date, tr: Translate): string | null {
    if (!iso) return null;
    const when = new Date(iso);
    if (Number.isNaN(when.getTime())) return null;
    const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
    const days = Math.round((startOfDay(now) - startOfDay(when)) / DAY_MS);
    if (days <= 0) return tr("topics.seenToday");
    if (days === 1) return tr("topics.seenYesterday");
    if (days < 7) return tr("topics.seenDaysAgo", { count: days });
    if (days < 30) return tr("topics.seenWeeksAgo", { count: Math.floor(days / 7) });
    if (days < 365) return tr("topics.seenMonthsAgo", { count: Math.floor(days / 30) });
    return tr("topics.seenYearsAgo", { count: Math.floor(days / 365) });
}

/** One short line per topic; the why line and the notes open on More. */
function TopicLabel({ topic, tr }: { topic: UserTopic; tr: Translate }) {
    const [expanded, setExpanded] = useState(false);
    const seen = topic.lastSeenAt ? describeTopicTime(topic.lastSeenAt, new Date(), tr) : null;
    const notes = [
        topic.kind ? tr(`topics.kinds.${topic.kind}`) : null,
        topic.pinned ? tr("topics.pinned") : null,
        topic.original ? tr("topics.renamedFrom", { original: topic.original }) : null,
        seen ? tr("topics.seen", { when: seen }) : null,
    ].filter(Boolean);
    const hasMore = Boolean(topic.why) || notes.length > 0;
    return (
        <div className="min-w-0 flex-1">
            <div className="flex items-baseline gap-2">
                <div className="min-w-0 truncate text-[13px] font-medium text-ink">{topic.label}</div>
                {hasMore && (
                    <button
                        type="button"
                        aria-expanded={expanded}
                        className="shrink-0 text-[12px] text-muted transition-colors hover:text-ink"
                        onClick={() => measureToday("T53", "open", "opened", () => setExpanded((open) => !open))}
                    >
                        {expanded ? tr("topics.less") : tr("topics.more")}
                    </button>
                )}
            </div>
            {expanded && (
                <>
                    {topic.why && <div className="mt-0.5 text-[12px] leading-snug text-muted">{topic.why}</div>}
                    {notes.length > 0 && <div className="mt-0.5 text-[11px] text-muted/80">{notes.join(" · ")}</div>}
                </>
            )}
        </div>
    );
}
