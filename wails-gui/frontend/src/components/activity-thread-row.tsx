import { useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { ChevronDown, PencilLine, Split } from 'lucide-react';
import { ActivityInlineNotice, errorNotice } from './activity-inline-notice';
import {
    MoveActivityToThread,
    RenameActivityThread,
    type ActivityThreadView,
    type ActivityView,
} from '@/lib/wails-shim-app';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Button } from '@/components/ui/button';
import { AppGlyph, useAppIcons } from './activities-view';
import { cn } from '../lib/utils';
import { formatDurationMs, formatTimeFromMs, formatTimeRangeFromMs } from './timeline/useCalendarData';

// "What you worked on" threads: Go groups a day's moments into threads
// (activity_threads.go); this row only shows what Go returned. Opening a thread
// lists its moments, each the same row the page shows for a single moment.

const rowTimeClass = 'w-14 shrink-0 text-[11px] text-muted/80 tabular-nums pt-[2px]';

/** Thread rows show the clock span and the time spent (Go's active time,
 *  which adds up to the day's "at the computer" line). The day line counts
 *  reading time since backlog item 70, so RC 1 shows it on every thread. */
export const SHOW_THREAD_TIME_SPENT = true;

/** The line under a thread's title: its time and active time, "10:02–10:30 AM
 *  · 28m active" (activity review v1.0.6 AR40, AR48). */
export function threadMetaLine(
    t: TFunction<'activity'>,
    thread: ActivityThreadView,
    showTimeSpent = SHOW_THREAD_TIME_SPENT,
): string {
    return [
        formatTimeRangeFromMs(thread.startMs, thread.endMs),
        // Under a minute reads as "0m active"; the span says enough.
        showTimeSpent && thread.timeSpentMs >= 60_000
            ? t('threads.timeSpent', { duration: formatDurationMs(thread.timeSpentMs) })
            : null,
    ]
        .filter(Boolean)
        .join(' · ');
}
const actionClass =
    'inline-flex items-center gap-1 text-[11px] text-muted hover:text-ink transition-colors disabled:opacity-50';

/** The apps a thread's moments ran in, first seen first, without repeats. */
function threadApps(moments: ActivityView[]) {
    const seen = new Set<string>();
    const out: { appKey: string; appName: string }[] = [];
    for (const moment of moments) {
        for (const app of moment.apps) {
            const key = app.appKey || app.appName;
            if (seen.has(key)) continue;
            seen.add(key);
            out.push(app);
        }
    }
    return out;
}

/** Rename on a thread's Not right: the title field's state and the call.
 *  An empty title returns the thread to Isoform's generated title. */
export function useThreadRename(threadId: string) {
    const { t } = useTranslation('activity');
    const [renaming, setRenaming] = useState(false);
    const [busy, setBusy] = useState(false);
    const [failedTitle, setFailedTitle] = useState<string | null>(null);
    const rename = async (title: string) => {
        setBusy(true);
        setFailedTitle(null);
        try {
            await RenameActivityThread(threadId, title);
            setRenaming(false);
        } catch (err) {
            console.error('[activity] rename thread failed:', err);
            setFailedTitle(title);
        } finally {
            setBusy(false);
        }
    };
    const renameFailed = failedTitle === null ? null : (
        <ActivityInlineNotice notice={errorNotice(t('threads.renameFailed'), () => void rename(failedTitle))} className="pt-1" />
    );
    return { renaming, setRenaming, busy, rename, renameFailed };
}

/** The inline title field Rename thread opens: Enter saves, Escape cancels. */
export function ThreadRenameField({
    initial,
    busy,
    onSave,
    onCancel,
}: {
    initial: string;
    busy: boolean;
    onSave: (title: string) => void;
    onCancel: () => void;
}) {
    const { t } = useTranslation('activity');
    const [draft, setDraft] = useState(initial);
    return (
        <div className="flex items-center gap-2">
            <input
                type="text"
                autoFocus
                aria-label={t('threads.renameLabel')}
                className="flex-1 min-w-0 rounded-md border border-border bg-panel px-2 py-1 text-[12px] text-ink outline-none focus:border-ink/30"
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={(e) => {
                    if (e.key === 'Enter' && draft.trim()) onSave(draft);
                    if (e.key === 'Escape') onCancel();
                }}
            />
            <Button size="sm" onClick={() => onSave(draft)} disabled={busy || !draft.trim()}>
                {t('threads.save')}
            </Button>
            <Button size="sm" variant="ghost" onClick={onCancel}>
                {t('activityCard.cancel')}
            </Button>
        </div>
    );
}

export function ThreadWorkRow({
    thread,
    moments,
    open,
    onToggle,
    renderMoment,
    focusMomentId,
    onFocusMomentConsumed,
}: {
    thread: ActivityThreadView;
    moments: ActivityView[];
    open: boolean;
    onToggle: () => void;
    renderMoment: (moment: ActivityView, open: boolean, onToggle: () => void) => ReactNode;
    /** A moment to open inside the thread (a suggestion's "From ..." link). */
    focusMomentId?: string | null;
    onFocusMomentConsumed?: (momentId: string) => void;
}) {
    const { t } = useTranslation('activity');
    const [openMomentId, setOpenMomentId] = useState<string | null>(null);
    useEffect(() => {
        if (focusMomentId && moments.some((m) => m.id === focusMomentId)) {
            setOpenMomentId(focusMomentId);
            onFocusMomentConsumed?.(focusMomentId);
        }
    }, [focusMomentId, moments, onFocusMomentConsumed]);
    const apps = threadApps(moments);
    useAppIcons(apps.map((a) => a.appKey));
    const live = moments.some((m) => m.inProgress);
    const { renaming, setRenaming, busy, rename, renameFailed } = useThreadRename(thread.id);

    const meta = threadMetaLine(t, thread);

    return (
        <li>
            <button
                type="button"
                onClick={onToggle}
                aria-expanded={open}
                className="w-full flex items-start gap-3 px-4 py-3 text-left hover:bg-ink/[0.02] transition-colors"
            >
                <time className={rowTimeClass}>{formatTimeFromMs(thread.startMs)}</time>
                <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                        <span className="text-[13px] font-medium text-ink truncate">{thread.title}</span>
                        {live && (
                            <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-border px-2 py-0.5 text-[10px] font-medium text-muted">
                                <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-accent-2" />
                                {t('activityCard.live')}
                            </span>
                        )}
                    </div>
                    <p className="text-[12px] text-muted truncate mt-0.5">{meta}</p>
                </div>
                {apps.length > 0 && (
                    <span className="flex items-center shrink-0 pt-0.5">
                        {apps.slice(0, 4).map((app, i) => (
                            <span
                                key={app.appKey || app.appName}
                                title={app.appName}
                                className={cn('rounded-[3px] bg-panel ring-1 ring-panel', i > 0 && '-ml-1')}
                            >
                                <AppGlyph appKey={app.appKey} appName={app.appName} className="h-4 w-4" />
                            </span>
                        ))}
                    </span>
                )}
                <ChevronDown
                    className={`h-3.5 w-3.5 shrink-0 mt-1 text-muted/60 transition-transform ${open ? 'rotate-180' : ''}`}
                />
            </button>
            {open && (
                <div className="pb-2">
                    <div className="px-4 pb-2 pl-[4.25rem]">
                        {renaming ? (
                            <ThreadRenameField
                                initial={thread.title}
                                busy={busy}
                                onSave={(title) => void rename(title)}
                                onCancel={() => setRenaming(false)}
                            />
                        ) : renameFailed ? (
                            renameFailed
                        ) : (
                            <DropdownMenu>
                                <DropdownMenuTrigger asChild>
                                    <button type="button" disabled={busy} className={actionClass}>
                                        <PencilLine className="h-3 w-3" />
                                        {t('threads.notRight')}
                                    </button>
                                </DropdownMenuTrigger>
                                <DropdownMenuContent align="start">
                                    <DropdownMenuItem onSelect={() => setRenaming(true)}>
                                        {t('threads.rename')}
                                    </DropdownMenuItem>
                                    {thread.userTitled && (
                                        <DropdownMenuItem onSelect={() => void rename('')}>
                                            {t('threads.useGenerated')}
                                        </DropdownMenuItem>
                                    )}
                                </DropdownMenuContent>
                            </DropdownMenu>
                        )}
                    </div>
                    <ul className="ml-[3.25rem] border-t border-border/60 divide-y divide-border/60">
                        {moments.map((moment) =>
                            renderMoment(moment, openMomentId === moment.id, () =>
                                setOpenMomentId((current) => (current === moment.id ? null : moment.id)),
                            ),
                        )}
                    </ul>
                </div>
            )}
        </li>
    );
}

/** Not right on a moment: move it to another thread of its day, or split it
 *  into a thread of its own. Go stores the move as a correction. */
export function ThreadMoveControl({
    activityId,
    currentThreadId,
    threads,
}: {
    activityId: string;
    currentThreadId: string | null;
    threads: ActivityThreadView[];
}) {
    const { t } = useTranslation('activity');
    const [busy, setBusy] = useState(false);
    const [moveFailed, setMoveFailed] = useState<string | null>(null);
    const others = threads.filter((thread) => thread.id !== currentThreadId);
    const inSharedThread =
        currentThreadId !== null &&
        (threads.find((thread) => thread.id === currentThreadId)?.activityIds.length ?? 0) > 1;
    if (others.length === 0 && !inSharedThread) return null;
    const move = async (target: string) => {
        setBusy(true);
        setMoveFailed(null);
        try {
            await MoveActivityToThread(activityId, target);
        } catch (err) {
            console.error('[activity] move moment failed:', err);
            setMoveFailed(target);
        } finally {
            setBusy(false);
        }
    };
    return (
        <>
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <button type="button" disabled={busy} className={actionClass}>
                    <Split className="h-3 w-3" />
                    {t('threads.moveTo')}
                </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="max-w-[320px]">
                {others.map((thread) => (
                    <DropdownMenuItem key={thread.id} onSelect={() => void move(thread.id)}>
                        <span className="truncate">{thread.title}</span>
                    </DropdownMenuItem>
                ))}
                {inSharedThread && (
                    <>
                        {others.length > 0 && <DropdownMenuSeparator />}
                        <DropdownMenuItem onSelect={() => void move('')}>{t('threads.ownThread')}</DropdownMenuItem>
                    </>
                )}
            </DropdownMenuContent>
        </DropdownMenu>
        {moveFailed !== null && <ActivityInlineNotice notice={errorNotice(t('threads.moveFailed'), () => void move(moveFailed))} className="pt-1" />}
        </>
    );
}
