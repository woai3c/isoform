import { memo, useEffect, useId, useMemo, useRef, useState, type CSSProperties, type ReactNode, type RefObject } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { ChevronLeft, ChevronRight, Undo2 } from 'lucide-react';
import { ACTION_LINK, ActivityInlineNotice, errorNotice, type ActivityNotice } from './activity-inline-notice';
import {
    ConfirmActivityThreadLink,
    MoveActivityToThread,
    type ActivityThreadGap,
    type ActivityThreadLinkReason,
    type ActivityThreadStretch,
    type ActivityThreadView,
    type ActivityView,
} from '@/lib/wails-shim-app';
import { Button } from '@/components/ui/button';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuSub,
    DropdownMenuSubContent,
    DropdownMenuSubTrigger,
    DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { ThreadRenameField, useThreadRename } from './activity-thread-row';
import { cn } from '../lib/utils';
import { formatDurationMs, formatTimeFromMs, formatTimeRangeFromMs } from './timeline/useCalendarData';
import { clockTime } from '@/lib/brief-time';
import i18n from '@/i18n';

// RC 1 "Stitched by work" (docs/rc1/handoff.md section 2): the day's threads
// on one timeline, a thread's stretches on one row joined across the gaps Go
// reports, and one card per resumed thread. Grouping is Go's
// (activity_threads.go); this view only draws the stretches, gaps, link
// reasons and resume points GetDayThreads returns. "As recorded" is the Day
// view's existing list.

export type WorkedOnView = 'stitched' | 'recorded';

/** One thread as the timeline draws it. */
export interface StitchedThread {
    thread: ActivityThreadView;
    /** Go's stretches in time order; a live thread's last one runs to now. */
    stretches: ActivityThreadStretch[];
    gaps: ActivityThreadGap[];
    /** The thread's moments the day list has, in Go's order. */
    moments: ActivityView[];
    live: boolean;
    sourceLive?: boolean;
    interruption: boolean;
    continuesBefore?: boolean;
    continuesAfter?: boolean;
}

/** Keep ungrouped observations in the read model; the row policy folds short ones. */
function ungroupedThread(moment: ActivityView): ActivityThreadView {
    return {
        id: `moment:${moment.id}`,
        title: moment.title,
        kind: 'work',
        userTitled: false,
        startMs: moment.startMs,
        endMs: moment.endMs,
        timeSpentMs: moment.activeMs,
        activityIds: [moment.id],
        stretches: [{ start: moment.startMs, end: moment.endMs, activeMs: moment.activeMs, activityIds: [moment.id], resumePoint: null }],
        gaps: [],
        activeMs: moment.activeMs,
        linkReason: { kind: 'rule', keys: [], text: '' },
        interruption: false,
        reviewed: false,
        unfinished: false,
    };
}

/** A recorded row uses the same half-open local day as the stitched view. */
export function clipDayActivity(moment: ActivityView, start: number, end: number, isToday: boolean): ActivityView {
    return { ...moment, startMs: Math.max(start, moment.startMs), endMs: Math.min(end, moment.endMs),
        // Go supplies day-owned active time from source sessions and runs.
        activeMs: moment.activeMs,
        inProgress: isToday && moment.inProgress };
}

/** Go's threads and ungrouped observations, without changing membership. */
export function stitchThreads(
    threads: ActivityThreadView[],
    activities: ActivityView[],
    now: number,
    isToday: boolean,
    dayStart = -Infinity,
    dayEnd = Infinity,
): StitchedThread[] {
    const byId = new Map(activities.map((a) => [a.id, a] as const));
    const threaded = new Set(threads.flatMap((thread) => thread.activityIds));
    const all = [...threads, ...activities.filter((a) => !threaded.has(a.id)).map(ungroupedThread)];
    const out = all.map((thread): StitchedThread => {
        const moments = thread.activityIds
            .map((id) => byId.get(id))
            .filter((a): a is ActivityView => a !== undefined && a.endMs > dayStart && a.startMs < dayEnd)
            .map((moment) => clipDayActivity(moment, dayStart, dayEnd, isToday));
        const sourceLive = thread.activityIds.some((id) => byId.get(id)?.inProgress);
        const live = isToday && sourceLive;
        // A computer older than the stretch read model sends no stretches;
        // the thread is then one stretch from its start to its end.
        const stretches = (thread.stretches?.length
            ? [...thread.stretches]
            : [{ start: thread.startMs, end: thread.endMs, activeMs: thread.timeSpentMs, activityIds: thread.activityIds, resumePoint: null }]
        ).sort((a, b) => a.start - b.start);
        // A running thread's last stretch reaches now only while the person
        // is still at it: idle past Go's away rule is not focused work.
        const latest = stretches[stretches.length - 1];
        if (live && isToday && now - latest.end <= FOCUS_GAP_MS) {
            stretches[stretches.length - 1] = { ...latest, end: Math.max(latest.end, now) };
        }
        const clipped = stretches.filter((stretch) => stretch.end > dayStart && stretch.start < dayEnd)
            .map((stretch) => ({ ...stretch, start: Math.max(dayStart, stretch.start), end: Math.min(dayEnd, stretch.end),
                activeMs: stretch.activeMs,
                resumePoint: stretch.end > dayEnd ? null : stretch.resumePoint }));
        const wasClipped = stretches.some((stretch) => stretch.start < dayStart || stretch.end > dayEnd);
        const gaps = (thread.gaps ?? []).filter((gap) => gap.end > dayStart && gap.start < dayEnd)
            .map((gap) => ({ ...gap, start: Math.max(dayStart, gap.start), end: Math.min(dayEnd, gap.end) }))
            .filter((gap) => !wasClipped || (clipped.length > 1 && gap.start >= clipped[0].end && gap.end <= clipped[clipped.length - 1].start));
        const activeMs = thread.activeMs ?? thread.timeSpentMs;
        return { thread: { ...thread, startMs: clipped[0]?.start ?? dayStart, endMs: clipped[clipped.length - 1]?.end ?? dayEnd, activeMs, timeSpentMs: activeMs },
            stretches: clipped, gaps, moments, live, sourceLive, interruption: !!thread.interruption,
            // Go draws a carried thread's stretches inside this day only; its
            // first moment's start still says where it began.
            continuesBefore: thread.startMs < dayStart || stretches.some((stretch) => stretch.start < dayStart),
            continuesAfter: thread.endMs > dayEnd || stretches.some((stretch) => stretch.end > dayEnd) };
    });
    const first = (s: StitchedThread) => s.stretches[0]?.start ?? s.thread.startMs;
    return out.filter((item) => item.stretches.length > 0).sort((a, b) => activeTime(b) - activeTime(a) || first(a) - first(b));
}

const HOUR_MS = 3_600_000;
/** Go's away rule (activityThreadFocusGapMs): longer idle ends a stretch. */
const FOCUS_GAP_MS = 10 * 60_000;
/** An interruption is 10 minutes or more away from the work (activity
 *  review v1.0.3 #1); a shorter hop is a plain gap: no link, no Away. */
const INTERRUPTION_MS = 10 * 60_000;

export function isInterruption(gap: ActivityThreadGap): boolean {
    return !gap.idle && gap.end - gap.start >= INTERRUPTION_MS;
}

export function activeTime(item: StitchedThread): number {
    return item.thread.activeMs ?? item.thread.timeSpentMs;
}

/** One policy for the timeline, headline and sidebar. Aggregates are not threads. */
export function stitchedRows(items: StitchedThread[], resumeThreadId?: string) {
    const qualifies = (item: StitchedThread) => !item.thread.id.startsWith('moment:')
        && (activeTime(item) >= 5 * 60_000 || item.stretches.length >= 2);
    const sorted = items.filter(qualifies).sort((a, b) =>
        Number(b.thread.id === resumeThreadId) - Number(a.thread.id === resumeThreadId)
        || activeTime(b) - activeTime(a) || a.thread.startMs - b.thread.startMs,
    );
    return {
        threads: sorted.slice(0, 8),
        otherWork: sorted.slice(8),
        shortMoments: items.filter((item) => !qualifies(item)),
    };
}

export function splitThreadTitle(title: string) {
    const colon = title.indexOf(':');
    return colon < 0 ? { title, detail: '' } : { title: title.slice(0, colon).trim(), detail: title.slice(colon + 1).trim() };
}

/** Names stay distinct across the day, including while search hides a sibling. */
export function stitchedTitles(threads: ActivityThreadView[], t: TFunction<'activity'>): Map<string, string> {
    const ordered = [...threads].sort((a, b) => a.startMs - b.startMs || a.id.localeCompare(b.id));
    const heads = new Map<string, number>();
    for (const thread of ordered) {
        const head = splitThreadTitle(thread.title).title;
        heads.set(head, (heads.get(head) ?? 0) + 1);
    }
    const used = new Set<string>();
    const titles = new Map<string, string>();
    for (const thread of ordered) {
        const parts = splitThreadTitle(thread.title);
        const words = parts.detail.split(/\s+/);
        let detail = words.slice(0, 3).join(' ');
        if (Array.from(t('stitched.titleDetail', { title: parts.title, detail })).length > 32) detail = words.slice(0, 2).join(' ');
        const base = boundedThreadTitle((heads.get(parts.title) ?? 0) > 1 && parts.detail
            ? t('stitched.titleDetail', { title: parts.title, detail }) : parts.title);
        let title = base;
        for (let count = 2; used.has(title); count++) {
            const suffix = t('stitched.titleNumber', { title: '', count });
            title = t('stitched.titleNumber', { title: boundedThreadTitle(base, 32 - Array.from(suffix).length), count });
        }
        used.add(title);
        titles.set(thread.id, title);
    }
    return titles;
}

function boundedThreadTitle(title: string, limit = 32): string {
    const chars = Array.from(title);
    return chars.length <= limit ? title : chars.slice(0, limit - 1).join('').trimEnd() + '…';
}

/** Words already used to distinguish a lane do not repeat below its title. */
export function threadDetailRemainder(original: string, displayed: string): string {
    const detail = splitThreadTitle(original).detail;
    const words = detail.split(/\s+/);
    for (let count = Math.min(3, words.length); count > 0; count--) {
        const prefix = words.slice(0, count).join(' ');
        if (displayed.includes(prefix)) return words.slice(count).join(' ');
    }
    return detail;
}

/** Short hour ticks have their own translated format; detailed spans keep the Today clock. */
export function stitchedAxisClock(ms: number, language: string, t: TFunction<'activity'>, withPeriod = true): string {
    const hour = new Date(ms).getHours();
    // Chinese reads 24-hour on every page: "10:00 · 11:00 · 12:00" (activity review v1.0.6 AR42).
    if (language.startsWith('zh')) return t('stitched.axisHour', { hour });
    const period = language.startsWith('zh')
        ? hour < 6 ? 'early' : hour < 12 ? 'morning' : hour === 12 ? 'noon' : hour <= 18 ? 'afternoon' : 'evening'
        : hour < 12 ? 'morning' : 'afternoon';
    if (!withPeriod) return t('stitched.axisHour', { hour: (hour + 11) % 12 + 1 });
    return t('stitched.axisTime', { hour: (hour + 11) % 12 + 1, period: i18n.getFixedT(language, 'today')(`todayBrief.clock.period.${period}`) });
}

/** The shared Today clock owns both formatting and translation keys. */
export function stitchedClock(ms: number, language = 'en'): string {
    if (language.startsWith('zh')) return formatTimeFromMs(ms, language);
    return clockTime(new Date(ms), language, (key, options) =>
        String(i18n.t(key as never, { ...options, ns: 'today', lng: language })),
    );
}

/** One clipped range owns the header, moment span, bars and axis. */
export function timelineRange(items: StitchedThread[]) {
    const starts = items.flatMap((s) => s.stretches.map((st) => st.start));
    const ends = items.flatMap((s) => s.stretches.map((st) => st.end));
    const start = Math.min(...starts);
    const end = Math.max(start + 1, ...ends);
    return { start, end, step: 3, steps: Math.ceil((end - start) / (3 * HOUR_MS)) };
}

/** Hour ticks on the bars' own scale, only inside the window (activity
 * review v1.0.1 #1): every hour when the labels fit, else every other hour.
 * A label carries its period ("9 AM", "12 PM") when the period changes;
 * label(ms, false) may drop it ("10"). Text boxes stay inside the track. */
export function stitchedAxisTicks(start: number, end: number, trackWidth: number, label: (ms: number, withPeriod: boolean) => string) {
    const hours: number[] = [];
    const tick = new Date(start);
    if (tick.getMinutes() || tick.getSeconds() || tick.getMilliseconds()) tick.setHours(tick.getHours() + 1, 0, 0, 0);
    while (tick.getTime() <= end) {
        hours.push(tick.getTime());
        tick.setHours(tick.getHours() + 1);
    }
    const boxWidth = (text: string) => Math.min(trackWidth, Array.from(text).length * 8 + 4);
    const widest = Math.max(0, ...hours.map((at) => boxWidth(label(at, true))));
    const step = hours.length > 1 && (hours[1] - hours[0]) / (end - start) * trackWidth >= widest + 8 ? 1 : 2;
    let right = -8;
    let period = '';
    return hours.filter((_, i) => i % step === 0).flatMap((at) => {
        const full = label(at, true);
        const short = label(at, false);
        const text = short !== full && full.replace(short, '') === period ? short : full;
        const width = boxWidth(text);
        const left = Math.max(0, Math.min(trackWidth - width, (at - start) / (end - start) * trackWidth - width / 2));
        if (left < right + 8) return [];
        right = left + width;
        period = full.replace(short, '');
        return [{ at, text, left, width }];
    });
}

/** dayActiveMs is the day's one active time (the Your day line); without it,
 *  as while a search narrows the rows, the shown rows' time is summed. */
export function stitchedSubline(t: TFunction<'activity'>, items: StitchedThread[], dayActiveMs?: number, language = 'en'): string {
    const range = timelineRange(items);
    return t('stitched.subline', {
        start: stitchedClock(range.start, language),
        end: stitchedClock(range.end, language),
        active: formatDurationMs(dayActiveMs ?? items.reduce((sum, item) => sum + activeTime(item), 0)),
    });
}

/** "Linked because: ..." in words; "Picked back up" only after an interruption. Go sends the provenance;
 *  the sentence is written here and never decides grouping. */
export function linkReasonText(t: TFunction<'activity'>, reason: ActivityThreadLinkReason | undefined, language: string): string {
    let base = t('stitched.whyFallback');
    if (reason?.kind === 'user') {
        base = t('stitched.whyUser');
    } else if (reason?.kind === 'model' && reason.text.trim()) {
        base = reason.text.trim();
    } else if (reason?.kind === 'rule') {
        const counts = new Map<string, number>();
        for (const key of reason.keys ?? []) {
            const kind = key.slice(0, key.indexOf(':'));
            if (kind === 'pr' || kind === 'file' || kind === 'person' || kind === 'channel' || kind === 'site') {
                counts.set(kind, (counts.get(kind) ?? 0) + 1);
            }
        }
        if (counts.size > 0) {
            const nouns = [...counts].map(([kind, count]) => t(`stitched.noun.${kind as 'pr'}`, { count }));
            base = t('stitched.whySame', { things: joinList(nouns, language) });
        }
    }
    return reason?.resumedWithinMinutes != null && reason.resumedWithinMinutes * 60_000 >= INTERRUPTION_MS
        ? t('stitched.whyResumed', { reason: base, minutes: reason.resumedWithinMinutes })
        : t('stitched.why', { reason: base });
}

function joinList(items: string[], language: string): string {
    try {
        return new Intl.ListFormat(language, { style: 'long', type: 'conjunction' }).format(items);
    } catch {
        return items.join(', ');
    }
}

/** The headline over Stitched by work, or null while nothing is drawn. */
// A resume requires a positive gap between two visible stretches. Clipping a
// thread to a calendar day must not turn the boundary into an interruption.
function resumeIndexes(item: StitchedThread): number[] {
    return item.stretches.flatMap((stretch, index) => index > 0 && item.gaps.some((gap) =>
        isInterruption(gap) && gap.start === item.stretches[index - 1].end && gap.end === stretch.start,
    ) ? [index] : []);
}

export function stitchedHeadline(t: TFunction<'activity'>, items: StitchedThread[], resumeThreadId?: string): string | null {
    if (items.length === 0) return null;
    const rows = stitchedRows(items, resumeThreadId).threads;
    const count = rows.length;
    const resumed = rows.filter((s) => resumeIndexes(s).length > 0).length;
    if (resumed > 0) return t('stitched.headlineSomeResumed', { count, resumed });
    return t('stitched.headline', { count });
}

/** Recent substantial work with no activity since its last stretch ended.
 *  The key changes when the thread gets a new stretch. */
export interface PickUpNotice {
    key: string;
    threadId: string;
    title: string;
    steppedAwayMs: number;
    /** Where the stretch you stepped away from began. */
    fromMs: number;
    label: string;
    momentId: string;
    momentTitle: string;
    momentStartMs: number;
    activeMs: number;
}

export function pickUpNotice(items: StitchedThread[], dateStr: string, now: number): PickUpNotice | null {
    let best: StitchedThread | null = null;
    for (const item of items) {
        if (item.thread.id.startsWith('moment:') || activeTime(item) < 10 * 60_000 || item.interruption || item.live || item.sourceLive || item.continuesAfter) continue;
        const end = item.stretches[item.stretches.length - 1].end;
        if (end > now || now - end > 4 * HOUR_MS) continue;
        // A stale stretch projection must not offer work which has since resumed.
        if (item.thread.endMs > end || item.moments.some((moment) => moment.endMs > end)) continue;
        if (!best || activeTime(item) > activeTime(best)) best = item;
    }
    if (!best) return null;
    const last = best.stretches[best.stretches.length - 1];
    // The stretch you stepped away from runs back across plain gaps.
    let first = best.stretches.length - 1;
    while (first > 0 && !best.gaps.some((gap) => isInterruption(gap)
        && gap.start >= best!.stretches[first - 1].end && gap.end <= best!.stretches[first].start)) first--;
    const stretch = best.stretches.slice(first);
    const lastId = last.activityIds[last.activityIds.length - 1];
    const title = splitThreadTitle(best.thread.title);
    const moment = best.moments.find((m) => m.id === lastId);
    return {
        key: `${dateStr}:${best.thread.id}:${last.end}`,
        threadId: best.thread.id,
        title: [title.title.split(',')[0].trim(), title.detail.split(',')[0].trim()].filter(Boolean).join(' · '),
        steppedAwayMs: last.end,
        fromMs: stretch[0].start,
        label: last.resumePoint?.label ?? '',
        momentId: moment?.id ?? '',
        momentTitle: moment?.title ?? '',
        momentStartMs: moment?.startMs ?? 0,
        activeMs: stretch.reduce((sum, item) => sum + item.activeMs, 0),
    };
}

export function pickUpBody(t: TFunction<'activity'>, notice: PickUpNotice, language = 'en'): string {
    return t('stitched.pickUp.steppedAway', {
        time: stitchedClock(notice.steppedAwayMs, language),
        minutes: Math.floor(notice.activeMs / 60_000),
    });
}

// "Later" hides a pick-up for the rest of the app session.
const laterPickUps = new Set<string>();
export function isPickUpLater(key: string): boolean {
    return laterPickUps.has(key);
}
export function setPickUpLater(key: string): void {
    laterPickUps.add(key);
}
export function resetPickUpLater(): void {
    laterPickUps.clear();
}

// ── The view ──

export interface ActivityStitchedViewProps {
    /** Threads to draw (search applied). */
    items: StitchedThread[];
    /** Every thread of the day: Not right's move targets. */
    threads: ActivityThreadView[];
    status: 'loading' | 'failed' | 'linking' | 'ready';
    query: string;
    selectedThreadId: string | null;
    onSelectThread: (threadId: string | null) => void;
    /** A thread whose card should scroll into view (Show where you left off). */
    scrollThreadId: string | null;
    onScrolled: () => void;
    pickUp: PickUpNotice | null;
    resumeThreadId?: string;
    onPickUpShow: (notice: PickUpNotice) => void;
    onPickUpLater: (notice: PickUpNotice) => void;
    onRetry: () => void;
    /** Shown instead of the empty line when there is nothing and no search
     *  (today on the first days: what Isoform sees right now). */
    empty?: ReactNode;
}

export const ActivityStitchedView = memo(function ActivityStitchedView({
    items,
    threads,
    status,
    query,
    selectedThreadId,
    onSelectThread,
    scrollThreadId,
    onScrolled,
    pickUp,
    resumeThreadId,
    onPickUpShow,
    onPickUpLater,
    onRetry,
    empty,
}: ActivityStitchedViewProps) {
    const { t } = useTranslation('activity');
    const workHeadingId = useId();

    useEffect(() => {
        if (!scrollThreadId) return;
        const frame = requestAnimationFrame(() => {
            const card = Array.from(document.querySelectorAll<HTMLElement>('[data-thread-card]')).find(
                (el) => el.dataset.threadCard === scrollThreadId,
            );
            const lane = Array.from(document.querySelectorAll<HTMLElement>('[data-thread-lane]')).find(
                (el) => el.dataset.threadLane === scrollThreadId,
            );
            (card ?? lane)?.scrollIntoView({ block: 'nearest' });
            onScrolled();
        });
        return () => cancelAnimationFrame(frame);
    }, [scrollThreadId, onScrolled]);

    const work = useMemo(() => items.filter((s) => s.stretches.length >= 2
        || (!s.thread.id.startsWith('moment:') && (s.continuesBefore || s.continuesAfter))), [items]);
    const dayThreads = useMemo(
        () => new Map([...threads, ...items.map((item) => item.thread)].map((thread) => [thread.id, thread])),
        [threads, items],
    );
    const titles = useMemo(() => stitchedTitles([...dayThreads.values()], t), [dayThreads, t]);

    if (status === 'failed') return <StitchedFailure onRetry={onRetry} />;
    if (status === 'loading') return <StitchedLoading />;
    // While Go links the day, ungrouped observations stay in Short moments.
    const linking =
        status === 'linking' ? (
            <div className={cn(STATE_CARD, 'gap-2.5')} role="status">
                <span className="text-[13.5px] text-ink">{t('stitched.linking')}</span>
                <span className="text-[12.5px] text-muted">{t('stitched.linkingNote')}</span>
            </div>
        ) : null;
    if (items.length === 0) {
        if (linking) return linking;
        if (empty && !query) return <>{empty}</>;
        return (
            <p className="text-[12px] text-muted/70 px-1">
                {query ? t('day.noMatches', { query }) : t('day.sectionEmpty')}
            </p>
        );
    }
    return (
        <div className="space-y-7">
            {linking}
            {pickUp && <PickUpCard notice={pickUp} onShow={onPickUpShow} onLater={onPickUpLater} />}
            <StitchedTimeline
                items={items}
                titles={titles}
                resumeThreadId={resumeThreadId}
                selectedThreadId={selectedThreadId}
                onSelectThread={onSelectThread}
            />
            {work.length > 0 && (
                <section className="flex flex-col gap-3" aria-labelledby={workHeadingId}>
                    <h4 id={workHeadingId} className="text-[18px] font-semibold text-ink">
                        {t('stitched.workThreads')}
                    </h4>
                    {work.map((item) => (
                        <ThreadCard
                            key={item.thread.id}
                            item={item}
                            title={titles.get(item.thread.id) ?? item.thread.title}
                            threads={threads}
                            selected={selectedThreadId === item.thread.id}
                        />
                    ))}
                    <p className="text-[12.5px] text-muted">{t('stitched.notRightNote')}</p>
                </section>
            )}
        </div>
    );
});

const STATE_CARD = 'flex min-h-[96px] flex-col rounded-xl border border-border bg-panel p-[18px]';
// A failure card hugs its one line (activity review v1.0.6 AR47).
const FAILURE_CARD = 'rounded-xl border border-border bg-panel px-[18px] py-3';

function StitchedLoading() {
    const { t } = useTranslation('activity');
    return (
        <div className={cn(STATE_CARD, 'gap-2.5')} role="status" aria-busy="true" aria-label={t('stitched.loading')} data-testid="stitched-loading">
            {[0, 30, 10].map((offset) => (
                <div key={offset} className="flex items-center gap-2.5">
                    <span className="h-2.5 w-[90px] shrink-0 rounded-md bg-surface-active" />
                    <span className="h-4 flex-1 rounded-md bg-surface-active" style={{ marginLeft: `${offset}%` }} />
                </div>
            ))}
        </div>
    );
}

/** A read that failed says so; it never looks like an empty day. */
export function StitchedFailure({ onRetry }: { onRetry: () => void }) {
    const { t } = useTranslation('activity');
    return (
        <div className={FAILURE_CARD}>
            <p role="alert" className="text-[13.5px] text-copper">
                {t('stitched.loadFailed')}{' '}
                <button type="button" className={ACTION_LINK} onClick={onRetry}>
                    {t('stitched.retry')}
                </button>
            </p>
        </div>
    );
}

function PickUpCard({
    notice,
    onShow,
    onLater,
}: {
    notice: PickUpNotice;
    onShow: (notice: PickUpNotice) => void;
    onLater: (notice: PickUpNotice) => void;
}) {
    const { t, i18n } = useTranslation('activity');
    return (
        <div
            data-testid="stitched-pick-up"
            className="flex flex-wrap items-center gap-x-4 gap-y-3 rounded-xl border border-border bg-panel px-[18px] py-3.5 shadow-card"
        >
            <Undo2 className="size-[18px] shrink-0 text-muted" aria-hidden />
            <div className="flex min-w-0 flex-[1_1_320px] flex-col gap-0.5">
                <span className="text-[11px] font-medium uppercase tracking-[0.08em] text-muted">
                    {t('stitched.pickUp.label')}
                </span>
                <span className="text-[14px] font-semibold text-ink">{t('stitched.pickUp.title', { title: notice.title })}</span>
                <span className="text-[13px] text-muted">{pickUpBody(t, notice, i18n.language)}</span>
            </div>
            <Button size="sm" onClick={() => onShow(notice)}>
                {t('stitched.pickUp.show')}
            </Button>
            <Button size="sm" variant="quiet" onClick={() => onLater(notice)}>
                {t('stitched.pickUp.later')}
            </Button>
        </div>
    );
}

// ── Timeline ──

const LABEL_COLUMN = 'w-[200px] min-w-[200px] min-[1440px]:w-[240px] shrink-0';
const GAP_COLUMN = 'w-[170px] shrink-0 pl-3 pr-2';

export function gapVisible(gap: ActivityThreadGap, span: number, trackWidth: number): boolean {
    return (gap.end - gap.start) / span * trackWidth >= 4;
}

function gapDuration(t: TFunction<'activity'>, ms: number): string {
    const minutes = Math.floor(ms / 60_000);
    const hours = Math.floor(minutes / 60);
    return hours > 0
        ? minutes % 60 ? t('stitched.durationHoursMinutes', { hours, minutes: minutes % 60 }) : t('stitched.durationHours', { hours })
        : t('stitched.durationMinutes', { minutes });
}

export function rowGapLabel(t: TFunction<'activity'>, item: StitchedThread, items: StitchedThread[]): string {
    const gaps = item.gaps.filter(isInterruption);
    if (gaps.length === 0) return '';
    if (gaps.length > 1) return t('stitched.gapAwayInAll', {
        duration: gapDuration(t, gaps.reduce((sum, gap) => sum + gap.end - gap.start, 0)),
    });
    const gap = gaps[0];
    const label = interrupter(gap, items.map((other) => other.thread), item.thread.id);
    return label ? t('stitched.gapAway', { duration: formatDurationMs(gap.end - gap.start), label: splitThreadTitle(label).title })
        : t('stitched.gapAwayPlain', { duration: formatDurationMs(gap.end - gap.start) });
}

/** What took you away: Go's label, else the drawn work that filled most of
 *  the gap. Nothing filled it only when the computer was locked or asleep
 *  (activity review v1.0.3 #2). */
function interrupter(gap: ActivityThreadGap, threads: ActivityThreadView[], threadId: string): string {
    const named = threads.find((thread) => thread.id === gap.threadId)?.title
        || (gap.label === 'away' ? '' : gap.label);
    if (named) return named;
    let best = '';
    let bestMs = 0;
    for (const thread of threads) {
        if (thread.id === threadId) continue;
        const ms = (thread.stretches ?? []).reduce((sum, stretch) => sum + Math.max(0, Math.min(gap.end, stretch.end) - Math.max(gap.start, stretch.start)), 0);
        if (ms > bestMs) {
            bestMs = ms;
            best = thread.title;
        }
    }
    return best;
}

function StitchedTimeline({
    items,
    titles,
    resumeThreadId,
    selectedThreadId,
    onSelectThread,
}: {
    items: StitchedThread[];
    titles: Map<string, string>;
    resumeThreadId?: string;
    selectedThreadId: string | null;
    onSelectThread: (threadId: string | null) => void;
}) {
    const { t, i18n } = useTranslation('activity');
    const headingId = useId();
    const trackRef = useRef<HTMLDivElement>(null);
    const [trackWidth, setTrackWidth] = useState(0);
    useEffect(() => {
        const track = trackRef.current;
        if (!track) return;
        const measure = () => setTrackWidth(track.getBoundingClientRect().width);
        measure();
        if (typeof ResizeObserver === 'undefined') {
            window.addEventListener('resize', measure);
            return () => window.removeEventListener('resize', measure);
        }
        const observer = new ResizeObserver(measure);
        observer.observe(track);
        return () => observer.disconnect();
    }, []);
    const range = useMemo(() => timelineRange(items), [items]);
    const span = range.end - range.start;
    const pct = (ms: number) => `${(((ms - range.start) / span) * 100).toFixed(3)}%`;
    const width = (ms: number) => `${((ms / span) * 100).toFixed(3)}%`;
    const rows = useMemo(() => stitchedRows(items, resumeThreadId), [items, resumeThreadId]);
    const rowClass = (id: string) => cn(
        'flex min-h-[56px] w-full items-center rounded-md border-t border-border-divider py-2 text-left',
        'transition-[background-color,opacity] duration-[120ms] hover:bg-surface-hover',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus',
        selectedThreadId === id && 'bg-surface-active',
        selectedThreadId && selectedThreadId !== id && 'opacity-[0.55]',
    );
    const select = (id: string) => onSelectThread(selectedThreadId === id ? null : id);
    const segmentStyle = (start: number, end: number): CSSProperties => ({
        left: `min(${pct(start)}, calc(100% - 6px))`,
        width: `max(${width(end - start)}, 6px)`,
        maxWidth: `calc(100% - min(${pct(start)}, calc(100% - 6px)))`,
    });
    // Everything string-valued the timeline draws, computed once per data
    // change instead of on every render (selection, parent timers, search).
    const language = i18n.language;
    const ticks = useMemo(() => stitchedAxisTicks(range.start, range.end, trackWidth || 300,
        (at, withPeriod) => stitchedAxisClock(at, language, t, withPeriod)), [range.start, range.end, trackWidth, language, t]);
    // Hour lines only at the labeled hours, on the bars' scale (activity
    // review v1.0.3 #3).
    const hourLines = ticks.filter(({ at }) => at > range.start && at < range.end).map(({ at }) => (
        <span key={`hour-${at}`} aria-hidden data-testid="stitched-hour-line" className="absolute inset-y-0 w-px" style={{ left: pct(at), backgroundColor: 'var(--color-border-subtle)' }} />
    ));
    const threadViews = useMemo(() => items.map((other) => other.thread), [items]);
    const laneModels = useMemo(() => rows.threads.map((item) => {
        const id = item.thread.id;
        const title = splitThreadTitle(item.thread.title);
        const detail = threadDetailRemainder(item.thread.title, titles.get(id) ?? title.title);
        const meta = detail
            ? t('stitched.laneDetailMeta', { detail, active: formatDurationMs(activeTime(item)) })
            : t('stitched.laneMeta', { active: formatDurationMs(activeTime(item)) });
        const threadSpan = formatTimeRangeFromMs(item.stretches[0].start, item.stretches[item.stretches.length - 1].end, i18n.language);
        const laneTip = t('stitched.segmentTip', { title: item.thread.title, span: threadSpan });
        const activeSuffix = detail ? t('stitched.laneActiveSuffix', { active: formatDurationMs(activeTime(item)) }) : meta;
        // Idle inside the same work splits the bar but is no resume: no link.
        const gaps = item.gaps.filter((gap) => isInterruption(gap) && gapVisible(gap, span, trackWidth)).map((gap) => {
            const label = interrupter(gap, threadViews, id);
            const tip = t(label ? 'stitched.gapTip' : 'stitched.gapTipPlain', {
                duration: formatDurationMs(gap.end - gap.start),
                span: formatTimeRangeFromMs(gap.start, gap.end, i18n.language), label,
            });
            return { gap, tip };
        });
        const stretches = item.stretches.map((stretch) => ({
            stretch,
            tip: t('stitched.segmentTip', { title: item.thread.title, span: formatTimeRangeFromMs(stretch.start, stretch.end, i18n.language) }),
        }));
        const gapSummary = rowGapLabel(t, item, items);
        return { item, id, title, detail, meta, threadSpan, laneTip, activeSuffix, gaps, stretches, gapSummary };
        // trackWidth only decides which gaps clear the 4 px minimum.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }), [rows, titles, items, threadViews, language, t, trackWidth]);
    const aggregateModels = useMemo(() => [
        { id: 'aggregate:other', members: rows.otherWork, short: false },
        { id: 'aggregate:short', members: rows.shortMoments, short: true },
    ].filter((group) => group.members.length > 0).map(({ id, members, short }) => {
        const entries = (short ? members.flatMap((item) => item.moments.length ? item.moments.map((moment) => ({ id: moment.id, title: moment.title, start: moment.startMs, end: moment.endMs }))
            : [{ id: item.thread.id, title: item.thread.title, start: item.thread.startMs, end: item.thread.endMs }])
            : members.map((item) => ({ id: item.thread.id, title: item.thread.title, start: item.thread.startMs, end: item.thread.endMs })))
            .sort((a, b) => a.start - b.start);
        const label = t(short ? 'stitched.shortMoments' : 'stitched.otherWork', {
            count: entries.length, minutes: Math.floor(members.reduce((sum, item) => sum + activeTime(item), 0) / 60_000),
        });
        const ticks = members.flatMap((item) => (short && item.moments.length ? item.moments.map((moment) => ({ start: moment.startMs, end: moment.endMs })) : item.stretches)
            .map((stretch) => ({ ...stretch, interruption: item.interruption })));
        return { id, short, entries, label, ticks };
    }), [rows, t]);
    return (
        <section aria-labelledby={headingId} className="flex flex-col gap-2.5 rounded-xl border border-border bg-panel px-5 pt-[18px] pb-4">
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1.5">
                <h4 id={headingId} className="text-[16px] font-semibold text-ink tabular-nums">
                    {formatTimeRangeFromMs(range.start, range.end, i18n.language)}
                </h4>
                <span className="text-[12.5px] text-muted">{t('stitched.timelineNote')}</span>
                <div className="ml-auto flex flex-wrap gap-3.5 text-[12px] text-muted" data-testid="stitched-legend">
                    <span className="inline-flex items-center gap-1.5"><span className="h-2 w-3.5 rounded-[3px] bg-ink" aria-hidden />{t('stitched.legendWork')}</span>
                    <span className="inline-flex items-center gap-1.5"><span className="h-2 w-3.5 rounded-[3px] bg-faint" aria-hidden />{t('stitched.legendInterruption')}</span>
                    <span className="inline-flex items-center gap-1.5"><span className="w-3.5 border-t-2 border-dashed border-accent" aria-hidden />{t('stitched.legendResumed')}</span>
                </div>
            </div>
            <div className="-mx-5 overflow-x-auto" data-testid="stitched-scroller">
                <div className="flex min-w-[800px] flex-col px-5">
                    <div className="flex pb-1.5" aria-hidden>
                        <div className={LABEL_COLUMN} />
                        <div ref={trackRef} data-testid="stitched-axis" className="relative h-4 min-w-[300px] flex-1 text-[11.5px] text-muted tabular-nums">
                            {ticks.map(({ at, text, left, width }) => (
                                <span key={at} className="absolute whitespace-nowrap text-center" style={{ left, width }}>
                                    {text}
                                </span>
                            ))}
                        </div>
                        <div className={GAP_COLUMN} />
                    </div>
                    {laneModels.map(({ item, id, title, detail, meta, laneTip, activeSuffix, gaps, stretches, gapSummary }) => {
                        const selected = selectedThreadId === id;
                        return (
                            <button key={id} type="button" data-thread-lane={id} data-interruption={item.interruption ? 'true' : undefined}
                                aria-pressed={selected} onClick={() => select(id)} className={rowClass(id)}>
                                <span className={cn(LABEL_COLUMN, 'flex flex-col gap-px pl-2 pr-3')} title={laneTip}>
                                    <span className="line-clamp-2 break-words text-[13px] font-semibold text-ink" title={item.thread.title}>{titles.get(id) ?? title.title}</span>
                                    {item.continuesBefore && <span className="text-[11px] text-muted">{t('stitched.continuesBefore')}</span>}
                                    <span className="flex text-[11.5px] text-muted" title={meta}>
                                        {detail && <span className="truncate">{detail}</span>}
                                        <span className="shrink-0 whitespace-pre">{activeSuffix}</span>
                                    </span>
                                </span>
                                <span className="relative block h-[22px] min-w-[300px] flex-1 overflow-hidden">
                                    {hourLines}
                                    {gaps.map(({ gap, tip }) => (
                                        <span key={`gap-${gap.start}`} data-testid="stitched-gap-link" title={tip} aria-label={tip}
                                            className="absolute top-[10px] border-t-2 border-dashed border-accent" style={{ left: pct(gap.start), width: width(gap.end - gap.start) }} />
                                    ))}
                                    {stretches.map(({ stretch, tip }) => (
                                        <span key={`stretch-${stretch.start}`} data-testid="stitched-stretch"
                                            className={cn('absolute top-0 h-[22px] rounded-md', item.interruption ? 'bg-faint' : 'bg-ink')}
                                            style={segmentStyle(stretch.start, stretch.end)}
                                            title={tip}>
                                            {item.continuesBefore && stretch === item.stretches[0] && <ChevronLeft aria-label={t('stitched.continuesBefore')} className="absolute left-0 top-1 size-3.5 text-panel" />}
                                            {item.continuesAfter && stretch === item.stretches[item.stretches.length - 1] && <ChevronRight aria-label={t('stitched.continuesAfter')} className="absolute right-0 top-1 size-3.5 text-panel" />}
                                        </span>
                                    ))}
                                </span>
                                <span className={cn(GAP_COLUMN, 'text-[11.5px] leading-snug text-muted')} data-testid={gapSummary ? 'stitched-gap-label' : undefined}>{gapSummary}</span>
                            </button>
                        );
                    })}
                    {aggregateModels.map(({ id, entries, label, ticks: aggregateTicks }) => {
                        return (
                            <div key={id}>
                                <button type="button" data-aggregate-lane={id} className={rowClass(id)} aria-pressed={selectedThreadId === id} aria-expanded={selectedThreadId === id} onClick={() => select(id)}>
                                    <span className={cn(LABEL_COLUMN, 'px-2 text-[13px] font-semibold text-ink')}>{label}</span>
                                    <span className="relative block h-[22px] min-w-[300px] flex-1 overflow-hidden">
                                        {hourLines}
                                        {aggregateTicks.map((stretch, i) => (
                                            <span key={i} data-testid="stitched-tick" data-interruption={stretch.interruption ? 'true' : undefined} className={cn('absolute top-1 h-3.5 rounded-sm', stretch.interruption ? 'bg-faint' : 'bg-ink')} style={{ left: `min(${pct(stretch.start)}, calc(100% - 6px))`, width: '6px' }} />
                                        ))}
                                    </span>
                                    <span className={GAP_COLUMN} />
                                </button>
                                {selectedThreadId === id && (
                                    <ul aria-label={label} className="space-y-2 rounded-lg bg-panel-warm p-3">
                                        {entries.map((entry) => (
                                            <li key={entry.id} className="flex gap-3 text-[13px] text-ink">
                                                <span className="shrink-0 text-muted tabular-nums">{formatTimeRangeFromMs(entry.start, entry.end, i18n.language)}</span>
                                                <span>{entry.title}</span>
                                            </li>
                                        ))}
                                    </ul>
                                )}
                            </div>
                        );
                    })}
                </div>
            </div>
        </section>
    );
}

function gapLabel(t: TFunction<'activity'>, gap: ActivityThreadGap, threads: ActivityThreadView[], threadId: string): string {
    if (!isInterruption(gap)) return '';
    const label = interrupter(gap, threads, threadId);
    return label ? t('stitched.stripAway', { label: splitThreadTitle(label).title }) : t('stitched.stripAwayPlain');
}

// ── Thread card ──

/** The apps a thread's moments ran in, first seen first, without repeats. */
function threadAppNames(moments: ActivityView[]): string[] {
    const seen = new Set<string>();
    for (const moment of moments) {
        for (const app of moment.apps) seen.add(app.appName);
    }
    return [...seen];
}

/** "What" in Left off at and Picked back up is the thing you were in, as
 *  people say it (activity review v1.0.0 #8): a document's name, never a
 *  duration or a raw path. A file path becomes its file name; a path with no
 *  file (a folder, a URL path) says nothing a person would. */
export function pointWhat(text: string | undefined, kind?: string): string {
    const value = (text ?? '').trim();
    if (!value) return '';
    if (/^(?:(?:\d+(?:[.,]\d+)?)\s*(?:ms|s|sec|secs|seconds?|m|min|mins|minutes?|h|hr|hrs|hours?)\s*)+$/i.test(value)) return '';
    if (/^[\d\s:.,·-]+$/.test(value)) return '';
    const pathLike = (kind === 'path' && /[\\/]/.test(value)) || /^(?:~|\.{0,2})?\/\S*$/.test(value) || /^[a-z]:\\\S*$/i.test(value);
    if (!pathLike) return readableName(value) ? value : '';
    const name = value.replace(/[\\/]+$/, '').split(/[\\/]/).pop() ?? '';
    return /\.[a-z0-9]{1,8}$/i.test(name) && readableName(name) ? name : '';
}

const KNOWN_FILE_EXTENSIONS = new Set(('pdf doc docx xls xlsx csv tsv ppt pptx key pages numbers txt md markdown rtf json jsonl yaml yml toml ini cfg '
    + 'xml html htm css scss less js mjs cjs jsx ts tsx go py rb rs java kt kts swift c h cc cpp hpp m mm sh bash zsh fish sql '
    + 'png jpg jpeg gif svg webp heic tiff bmp ico mov mp4 m4v mkv webm mp3 wav m4a aac flac zip tar gz tgz bz2 xz 7z rar dmg pkg app '
    + 'sketch fig psd ai xd ipynb log lock mod sum proto graphql gql vue svelte php cs fs scala r dart lua pl ex exs erl hs ml clj '
    + 'tex bib epub mobi eml ics vcf plist strings xib storyboard xcodeproj gradle tf hcl dockerfile env').split(' '));

/** A name a person can read (activity review v1.0.7 AR50): text read off the
 *  screen can come back scrambled ("wnocishiscaeronaariva...niod",
 *  "apostla-ple-1-201g.par"). A file name needs an extension people use, and
 *  no word runs past 20 letters. */
export function readableName(value: string): boolean {
    const ext = /^\S+\.([a-z]{1,8})$/i.exec(value)?.[1];
    if (ext && !KNOWN_FILE_EXTENSIONS.has(ext.toLowerCase())) return false;
    return value.split(/[^\p{L}]+|(?<=\p{Ll})(?=\p{Lu})/u).every((word) => [...word].length <= 20);
}

/** Where a stretch ended or began: the thing you were in, never a summary
 *  of what you did (activity review v1.0.4), and never a string a person
 *  can't read; without a readable name, the app (activity review v1.0.7 AR50). */
export function stretchPoint(item: StitchedThread, stretch: ActivityThreadStretch, at: 'end' | 'start'): string {
    const id = at === 'end' ? stretch.activityIds[stretch.activityIds.length - 1] : stretch.activityIds[0];
    const moment = item.moments.find((m) => m.id === id);
    const app = moment?.apps[0]?.appName ?? '';
    if (at === 'end') {
        // Left off at is its resume point, else the app, else the time alone.
        return pointWhat(stretch.resumePoint?.label, stretch.resumePoint?.kind) || app;
    }
    // Picked back up names the thing you were in, never what you did there
    // (activity review v1.0.1 #3): the stretch's resume point, else a moment
    // title that differs from the thread's, else the app.
    return pointWhat(stretch.resumePoint?.label, stretch.resumePoint?.kind)
        || (moment && moment.title !== item.thread.title ? pointWhat(moment.title) : '')
        || app;
}

const chipTextWidth = (text: string) => [...text].reduce((sum, ch) => sum + (/[\u2e80-\u9fff\uff00-\uffef]/.test(ch) ? 12 : 6.6), 0);

/** Chips only when every stretch shows its full time (activity review
 *  v1.0.7 AR51). Each segment gets its flex share of the strip; a stretch
 *  needs its label plus padding. Away labels may still shorten. An
 *  unmeasured strip (width 0) keeps chips. */
export function stripChipsFit(segments: { kind: 'stretch' | 'gap'; grow: number; label: string }[], width: number): boolean {
    if (width <= 0 || segments.length === 0) return true;
    const total = segments.reduce((sum, segment) => sum + segment.grow, 0);
    const free = width - 2 * (segments.length - 1);
    return segments.every((segment) => segment.kind !== 'stretch' || (segment.grow / total) * free >= chipTextWidth(segment.label) + 14);
}

function useElementWidth(ref: RefObject<HTMLElement | null>): number {
    const [width, setWidth] = useState(0);
    useEffect(() => {
        const el = ref.current;
        if (!el) return;
        const measure = () => setWidth(el.clientWidth);
        measure();
        if (typeof ResizeObserver === 'undefined') return;
        const observer = new ResizeObserver(measure);
        observer.observe(el);
        return () => observer.disconnect();
    }, [ref]);
    return width;
}

const ThreadCard = memo(function ThreadCard({ item, title, threads, selected }: { item: StitchedThread; title: string; threads: ActivityThreadView[]; selected: boolean }) {
    const { t, i18n } = useTranslation('activity');
    const clock = (ms: number) => stitchedClock(ms, i18n.language);
    const { thread, stretches, gaps } = item;
    // Timeline labels may show only the leading segment. A full card keeps
    // the original title unless the day supplied a disambiguated name.
    const cardTitle = title === splitThreadTitle(thread.title).title ? thread.title : title;
    const { renaming, setRenaming, busy: renameBusy, rename, renameFailed } = useThreadRename(thread.id);
    const [busy, setBusy] = useState(false);
    // A confirmation holds for the members it was given to; Go clears it
    // when membership changes.
    const membership = thread.activityIds.join(',');
    const [confirmedFor, setConfirmedFor] = useState<string | null>(null);
    const reviewed = thread.reviewed || confirmedFor === membership;

    const first = stretches[0];
    const last = stretches[stretches.length - 1];
    const awayMs = gaps.filter(isInterruption).reduce((sum, gap) => sum + gap.end - gap.start, 0);
    const resumes = resumeIndexes(item);
    const apps = threadAppNames(item.moments).slice(0, 3);
    const stats = [
        t('stitched.stats', { count: resumes.length + 1, active: formatDurationMs(thread.activeMs ?? thread.timeSpentMs) }),
        awayMs >= 60_000 ? t('stitched.statsAway', { duration: formatDurationMs(awayMs) }) : null,
        apps.length > 0 ? apps.join(', ') : null,
    ]
        .filter(Boolean)
        .join(' · ');

    const stripRef = useRef<HTMLDivElement>(null);
    const stripWidth = useElementWidth(stripRef);
    const segments = stretches.flatMap((stretch, i) => {
        const gap = i > 0 ? gaps[i - 1] : undefined;
        const stretchSegment = {
            kind: 'stretch' as const, start: stretch.start, end: stretch.end,
            grow: Math.max(stretch.end - stretch.start, 60_000),
            label: t('stitched.stretchLabel', { time: clock(stretch.start), duration: formatDurationMs(stretch.activeMs) }),
        };
        return gap && gap.end > gap.start
            ? [{ kind: 'gap' as const, start: gap.start, end: gap.end, grow: gap.end - gap.start, label: gapLabel(t, gap, threads, thread.id) }, stretchSegment]
            : [stretchSegment];
    });
    const chipsFit = stripChipsFit(segments, stripWidth);

    const point = (stretch: ActivityThreadStretch, at: 'end' | 'start') => {
        const time = clock(at === 'end' ? stretch.end : stretch.start);
        const what = stretchPoint(item, stretch, at);
        return what ? t('stitched.pointAt', { time, what }) : time;
    };
    // With two or more stretches: where the latest gap began and where the
    // work resumed. One stretch: where it ended, unless it is still running.
    const latestResume = resumes[resumes.length - 1];
    // Idle inside the same work gets no Left off at or Picked back up.
    const idleOnly = latestResume === undefined && gaps.some((gap) => gap.idle);
    const leftOff = latestResume !== undefined ? point(stretches[latestResume - 1], 'end') : item.live || item.continuesAfter || idleOnly ? null : point(last, 'end');
    const pickedUp = latestResume !== undefined ? point(stretches[latestResume], 'start') : null;

    const [correctionNotice, setCorrectionNotice] = useState<ActivityNotice | null>(null);
    const confirm = async () => {
        setBusy(true);
        setCorrectionNotice(null);
        try {
            await ConfirmActivityThreadLink(thread.id);
            setConfirmedFor(membership);
        } catch (err) {
            console.error('[activity] confirm thread link failed:', err);
            setCorrectionNotice(errorNotice(t('stitched.correctionFailed'), () => void confirm()));
        } finally {
            setBusy(false);
        }
    };
    // Moving a stretch moves each of its moments; "its own thread" makes the
    // first one a new thread and moves the rest into it.
    const moveStretch = async (stretch: ActivityThreadStretch, target: string) => {
        setBusy(true);
        try {
            let dest = target;
            for (const id of stretch.activityIds) {
                const moved = await MoveActivityToThread(id, dest);
                if (!dest) dest = moved;
            }
        } catch (err) {
            console.error('[activity] move stretch failed:', err);
            setCorrectionNotice(errorNotice(t('stitched.correctionFailed'), () => void moveStretch(stretch, target)));
        } finally {
            setBusy(false);
        }
    };
    const others = threads.filter((other) => other.id !== thread.id);
    const canMove = others.length > 0 || stretches.length > 1;

    return (
        <article
            data-thread-card={thread.id}
            className={cn(
                'flex flex-col gap-3 rounded-xl border border-border px-5 py-[18px] transition-colors duration-[120ms]',
                // Selected is the design system's selected fill, never ink.
                selected ? 'bg-surface-active' : 'bg-panel',
            )}
        >
            <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2">
                <span className="text-[15px] font-semibold text-ink">{cardTitle}</span>
                {item.continuesBefore && <span data-testid="stitched-continues-before" className="text-[11px] text-muted">{t('stitched.continuesBefore')}</span>}
                {item.continuesAfter && <span data-testid="stitched-continues-after" className="text-[11px] text-muted">{t('stitched.continuesAfter')}</span>}
                {item.live && (
                    <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-border px-2 py-0.5 text-[10px] font-medium text-muted">
                        <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-accent-2" />
                        {t('activityCard.live')}
                    </span>
                )}
                <span className="ml-auto text-[12.5px] text-muted tabular-nums">
                    {formatTimeRangeFromMs(first.start, last.end, i18n.language)}
                </span>
            </div>
            <div className="flex flex-col gap-1.5">
                {chipsFit ? (
                <div ref={stripRef} className="flex h-[26px] gap-0.5 overflow-hidden rounded-md" data-testid="stitched-strip">
                    {segments.map((segment) => segment.kind === 'gap' ? (
                        <span
                            key={`gap-${segment.start}`}
                            data-work-away
                            className="flex min-w-[6px] items-center justify-center overflow-hidden whitespace-nowrap bg-panel-warm px-1.5 text-[11.5px] text-muted"
                            style={{ flex: `${segment.grow} 1 0` }}
                        >
                            <span className="min-w-0 truncate">{segment.label}</span>
                        </span>
                    ) : (
                        <span
                            key={`stretch-${segment.start}`}
                            data-work-stretch
                            className="flex min-w-[6px] items-center justify-center overflow-hidden whitespace-nowrap bg-ink px-1.5 text-[11.5px] text-background"
                            style={{ flex: `${segment.grow} 1 0` }}
                        >
                            <span className="min-w-0 truncate">{segment.label}</span>
                        </span>
                    ))}
                </div>
                ) : (
                // Too many stretches to label: a plain bar on the thread's own
                // scale, each stretch's time on hover.
                <div ref={stripRef} className="relative h-[26px] overflow-hidden rounded-md bg-panel-warm" data-testid="stitched-strip" data-plain-strip>
                    {segments.filter((segment) => segment.kind === 'stretch').map((segment) => (
                        <span
                            key={`stretch-${segment.start}`}
                            data-work-stretch
                            title={segment.label}
                            aria-label={segment.label}
                            className="absolute inset-y-0 rounded-[3px] bg-ink"
                            style={{
                                left: `${((segment.start - first.start) / Math.max(1, last.end - first.start)) * 100}%`,
                                width: `max(4px, ${((segment.end - segment.start) / Math.max(1, last.end - first.start)) * 100}%)`,
                            }}
                        />
                    ))}
                </div>
                )}
                <span className="text-[12.5px] text-muted">{stats}</span>
            </div>
            {(leftOff || pickedUp) && (
                <div className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,260px),1fr))] gap-2.5">
                    {leftOff && <PointBox label={t('stitched.leftOff')} text={leftOff} />}
                    {pickedUp && <PointBox label={t('stitched.pickedUp')} text={pickedUp} />}
                </div>
            )}
            {thread.activityIds.length > 1 && (
                <div className="flex flex-wrap items-center gap-2 border-t border-border-divider pt-2.5">
                    <span className="text-[12.5px] text-muted">
                        {linkReasonText(t, thread.linkReason, i18n.resolvedLanguage ?? i18n.language)}
                    </span>
                    <div className="ml-auto flex items-center gap-1.5">
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                                <Button size="sm" variant="quiet" disabled={busy || renameBusy}>
                                    {t('threads.notRight')}
                                </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end" className="max-w-[320px]">
                                <DropdownMenuItem onSelect={() => setRenaming(true)}>{t('threads.rename')}</DropdownMenuItem>
                                {thread.userTitled && (
                                    <DropdownMenuItem onSelect={() => void rename('')}>{t('threads.useGenerated')}</DropdownMenuItem>
                                )}
                                {canMove && <DropdownMenuSeparator />}
                                {canMove &&
                                    stretches.map((stretch) => (
                                        <DropdownMenuSub key={`move-${stretch.start}`}>
                                            <DropdownMenuSubTrigger>
                                                {stretches.length > 1
                                                    ? t('stitched.moveStretch', { time: clock(stretch.start) })
                                                    : t('threads.moveTo')}
                                            </DropdownMenuSubTrigger>
                                            <DropdownMenuSubContent className="max-w-[320px]">
                                                {others.map((other) => (
                                                    <DropdownMenuItem key={other.id} onSelect={() => void moveStretch(stretch, other.id)}>
                                                        <span className="truncate">{other.title}</span>
                                                    </DropdownMenuItem>
                                                ))}
                                                {stretches.length > 1 && (
                                                    <>
                                                        {others.length > 0 && <DropdownMenuSeparator />}
                                                        <DropdownMenuItem onSelect={() => void moveStretch(stretch, '')}>
                                                            {t('threads.ownThread')}
                                                        </DropdownMenuItem>
                                                    </>
                                                )}
                                            </DropdownMenuSubContent>
                                        </DropdownMenuSub>
                                    ))}
                            </DropdownMenuContent>
                        </DropdownMenu>
                        {reviewed ? (
                            <span className="px-1 text-[12.5px] text-muted">{t('stitched.confirmed')}</span>
                        ) : (
                            <Button size="sm" variant="secondary" onClick={() => void confirm()} disabled={busy}>
                                {t('stitched.looksRight')}
                            </Button>
                        )}
                    </div>
                    <ActivityInlineNotice notice={correctionNotice} className="pt-1.5" />
                </div>
            )}
            {renaming && (
                <ThreadRenameField
                    initial={thread.title}
                    busy={renameBusy}
                    onSave={(title) => void rename(title)}
                    onCancel={() => setRenaming(false)}
                />
            )}
            {renameFailed}
        </article>
    );
});

function PointBox({ label, text }: { label: string; text: string }) {
    return (
        <div className="flex min-w-0 flex-col gap-1 rounded-lg bg-panel-warm px-3 py-2.5">
            <span className="text-[11px] font-medium uppercase tracking-[0.08em] text-muted">{label}</span>
            <span className="line-clamp-2 text-[13px] text-ink">{text}</span>
        </div>
    );
}
