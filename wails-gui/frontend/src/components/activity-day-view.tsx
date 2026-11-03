import { memo, useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useFirstDay } from '@/hooks/useFirstDay';
import { useDoingNow } from '@/hooks/useDoingNow';
import { ChevronDown, ChevronRight, Trash2, Zap } from 'lucide-react';
import { toast } from 'sonner';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { GenerateDailySummary } from '../../wailsjs/go/main/App';
import {
    DeleteActivity,
    GetActivitiesInRange,
    GetDayThreads,
    GetRecentMeetingNotes,
    type ActivityOutcome,
    type ActivityRunFrame,
    type ActivityThreadView,
    type ActivityView,
    type MeetingNotesView,
} from '@/lib/wails-shim-app';
import { invalidateActivitySessionCache, lastActivityScroll, prefetchActivitySessionData, refreshActivitySessionData, rememberActivityScroll, updateActivitySessionData, useActivitySessionQuery, useActivitySessionQueryState } from './timeline/activity-session-cache';
import { activeInterval } from '@/lib/app-activity';
import { ThreadMoveControl, ThreadWorkRow } from './activity-thread-row';
import {
    ActivityStitchedView,
    clipDayActivity,
    StitchedFailure,
    isPickUpLater,
    pickUpNotice,
    setPickUpLater,
    stitchThreads,
    stitchedHeadline,
    stitchedRows,
    stitchedSubline,
    type PickUpNotice,
    type WorkedOnView,
} from './activity-stitched';
import type { main } from '../../wailsjs/go/models';
import { EntryDetail } from '@/components/entry-content';
import { AppGlyph, FrameLightbox, RunFrameThumb, useAppIcons } from './activities-view';
import { AISummaryCard, cleanSummaryText } from './activity-summary';
import { ActivityRowActions } from './activity-row-actions';
import { ActivityNoticed } from './activity-noticed';
import type { MobileBriefActivityTarget } from '@/lib/wails-shim-app';
import type { TodayChatContextRef } from '@/lib/today-chat-navigation';
import { formatTodayChatPrefill } from '@/lib/today-chat-prefill';
import { warmTodayChatDrafts } from '@/lib/today-chat-prepared';
import { MeetingNotesCard } from './activity-meeting-notes';
import { ActivityCaptureLine } from './activity-capture-line';
import { cn } from '../lib/utils';
import {
    dayEndMs,
    dayStartMs,
    formatDateStr,
    formatDurationMs,
    formatTimeFromMs,
    type CronRunWithJobName,
} from './timeline/useCalendarData';

// The Activity page's Day view: the day's digest ("Your day"), one merged
// list of what you worked on (activities, meetings, Isoform's scheduled
// runs), and what Isoform learned. Everything technical (run counts, keyword
// chips, raw run output) stays out of this view. What you worked on shows the
// day "Stitched by work" (activity-stitched.tsx, the RC 1 timeline) or "As
// recorded", the merged list itself.

// Shared empty fallbacks: a fresh `?? []` per render would change identity
// and defeat the memoized selectors and rows below.
const NO_ACTIVITIES: ActivityView[] = [];
const NO_THREADS: ActivityThreadView[] = [];
const NO_MEETINGS: MeetingNotesView[] = [];

/** The pick-up notice is rebuilt on every stitched/clock change but its
 *  fields are all scalars; reuse the previous object while nothing changed
 *  so downstream memo boundaries (the stitched view, its card) hold. */
function useStablePickUp(next: PickUpNotice | null): PickUpNotice | null {
    const ref = useRef<PickUpNotice | null>(null);
    const prev = ref.current;
    if (prev && next &&
        prev.key === next.key && prev.threadId === next.threadId && prev.title === next.title &&
        prev.steppedAwayMs === next.steppedAwayMs && prev.label === next.label &&
        prev.momentId === next.momentId && prev.momentTitle === next.momentTitle &&
        prev.momentStartMs === next.momentStartMs && prev.activeMs === next.activeMs) {
        return prev;
    }
    ref.current = next;
    return next;
}

// ── Helpers ──

/** The one-line plain-text result shown for a scheduled run, or null when the
 *  output is markup/JSON or spans several lines — those runs show only their
 *  title plus an "Open result" link to the automation detail dialog, so raw
 *  tags never land on the day page. */
export function displayableRunResult(summary: string | undefined): string | null {
    const text = (summary ?? '').trim();
    if (!text) return null;
    if (text.includes('\n')) return null;
    if (text.startsWith('<') || text.startsWith('{') || text.startsWith('[')) return null;
    if (text.includes('<news_json>')) return null;
    return text;
}

// Immutable helpers for tracking which dates have an in-flight generation.
// Returns the same instance when the operation is a no-op so React state
// equality prevents unnecessary re-renders (Set is reference-compared).
function addDate(set: ReadonlySet<string>, date: string): ReadonlySet<string> {
    if (set.has(date)) return set;
    const next = new Set(set);
    next.add(date);
    return next;
}

function removeDate(set: ReadonlySet<string>, date: string): ReadonlySet<string> {
    if (!set.has(date)) return set;
    const next = new Set(set);
    next.delete(date);
    return next;
}

const sectionLabelClass = 'text-[11px] uppercase tracking-[0.08em] font-semibold text-muted/80 mb-2 px-1';

const WORKED_ON_VIEWS: WorkedOnView[] = ['stitched', 'recorded'];

// ── Day view ──

/** The Day view's three sections. The Activity page's section panel shows one
 *  at a time; an undefined section renders all three stacked (the layout the
 *  Day view had before the panel). */
export type ActivityDaySection = 'yourDay' | 'workedOn' | 'learned';

export interface ActivityDayViewProps {
    date: Date;
    noticedFocus?: MobileBriefActivityTarget | null;
    onNoticedFocusConsumed?: (focus: MobileBriefActivityTarget) => void;
    /** A thread to open in "What you worked on" once its rows are loaded. */
    initialThreadId?: string | null;
    onInitialThreadConsumed?: (threadId: string) => void;
    searchQuery: string;
    bridgePort: number;
    /** Day data from the page-level useCalendarData. */
    dailyData: main.DailySummaryData | null;
    dayLoading: boolean;
    automationRuns: CronRunWithJobName[];
    knowledge: main.KnowledgeEntry[];
    memoryEntries: main.MemoryEntry[];
    hourRange: { start: number; end: number };
    maxHourly: number;
    reloadDailySummary: () => Promise<void>;
    onAskAboutDate?: (date: string, scope: 'daily' | 'weekly') => void;
    /** "Chat about this" on a moment: the prepared words go in the main chat
     *  box, unsent, with the moment as context (Today's route). */
    onStartChat?: (prompt: string, context: TodayChatContextRef) => void;
    /** Opens a capture session in the Screen timeline view (learned-entry
     *  session links). */
    onOpenSession: (sessionId: string) => void;
    /** Opens a scheduled run's detail dialog (the automation that ran). */
    onOpenRunDetail: (jobId: string) => void;
    /** When set, only this section renders (the Activity page's section panel
     *  shows one section at a time). Undefined renders all three stacked. */
    section?: ActivityDaySection;
    /** A suggestion's source link reveals its moment in What you worked on. */
    onSectionChange?: (section: ActivityDaySection) => void;
    /** Reports the day's row counts (unfiltered) so the section panel can
     *  label "What you worked on" and "What Isoform learned" with them. */
    onCountsChange?: (counts: { workedOn: number; learned: number }) => void;
    /** What you worked on: "Stitched by work" or "As recorded". The Activity
     *  page keeps the pick; without it the view keeps its own, Stitched first. */
    workedOnView?: WorkedOnView;
    onWorkedOnViewChange?: (view: WorkedOnView) => void;
}

export function ActivityDayView({
    date,
    noticedFocus,
    onNoticedFocusConsumed,
    initialThreadId,
    onInitialThreadConsumed,
    searchQuery,
    bridgePort,
    dailyData,
    dayLoading,
    automationRuns,
    knowledge,
    memoryEntries,
    hourRange,
    maxHourly,
    reloadDailySummary,
    onAskAboutDate,
    onStartChat: startChat,
    onOpenSession,
    onOpenRunDetail,
    section,
    onSectionChange,
    onCountsChange,
    workedOnView,
    onWorkedOnViewChange,
}: ActivityDayViewProps) {
    const { t } = useTranslation('activity');
    const dateStr = formatDateStr(date);
    const dayStart = dayStartMs(date);
    const dayEnd = dayEndMs(date);
    const isToday = dateStr === formatDateStr(new Date());
    const firstDay = useFirstDay();
    const onStartChat = useMemo(() => startChat ? (prompt: string, ref: TodayChatContextRef) =>
        startChat(prompt, ref.sourceKind === 'activity' ? { ...ref, dayStartMs: dayStart } : ref) : undefined, [startChat, dayStart]);

    // The page remounts on every open; restore the day's scroll offset (the
    // warm cache renders the content at full height on the first commit).
    const scrollRef = useRef<HTMLDivElement>(null);
    useLayoutEffect(() => {
        const saved = lastActivityScroll(`day:${dateStr}`);
        if (saved && scrollRef.current) scrollRef.current.scrollTop = saved;
    }, [dateStr]);

    // ── The day's composed activities (AI-titled sessions) ──
    const loadActivities = useCallback(() => GetActivitiesInRange(dayStart, dayEnd), [dayStart, dayEnd]);
    const activityState = useActivitySessionQueryState(`activities:${dateStr}`, loadActivities);
    const rawActivities = activityState.data ?? NO_ACTIVITIES;
    const activities = useMemo(() => rawActivities.filter((activity) => activity.endMs > dayStart && activity.startMs < dayEnd)
        .map((activity) => clipDayActivity(activity, dayStart, dayEnd, isToday)), [rawActivities, dayStart, dayEnd, isToday]);
    // A failed read says so; it never reads as an empty day.
    const activitiesFailed = activityState.error !== undefined && activityState.data === undefined;
    // A live session's duration counts up on the local minute clock below;
    // its rows follow capture events, applied at most every 5 minutes
    // (activity-session-cache), never by a refetch timer.
    const hasLiveActivity = activities.some((activity) => activity.inProgress);
    const loadThreads = useCallback(() => GetDayThreads(dayStart, dayEnd), [dayStart, dayEnd]);
    const threadState = useActivitySessionQueryState(`threads:${dateStr}`, loadThreads);
    const rawThreads = threadState.data?.threads ?? NO_THREADS;
    const threads = useMemo(() => rawThreads.filter((thread) => thread.endMs > dayStart && thread.startMs < dayEnd)
        .map((thread) => ({ ...thread, startMs: Math.max(dayStart, thread.startMs), endMs: Math.min(dayEnd, thread.endMs) })),
        [rawThreads, dayStart, dayEnd]);
    const threadsLoaded = !threadState.loading;
    const threadsFailed = threadState.error !== undefined && threadState.data === undefined;
    const handleActivityDeleted = useCallback((id: string) => {
        updateActivitySessionData<ActivityView[]>(`activities:${dateStr}`, (rows) => (rows ?? []).filter((row) => row.id !== id));
    }, [dateStr]);

    // "Chat about this" is prepared for every moment while the day loads, in
    // one read, so a click fills the chat box at once (the Today route).
    const chatWarmKey = useMemo(
        () =>
            onStartChat
                ? JSON.stringify(
                      activities
                          .filter((a) => !a.actionsWithheld)
                          .map((a) => ({
                              ref: { sourceKind: 'activity', sourceId: a.id, dayStartMs: dayStart },
                              signature: `${a.title}|${a.description}|${a.endMs}|${a.activeMs}|${dayStart}`,
                          })),
                  )
                : '',
        [activities, onStartChat, dayStart],
    );
    useEffect(() => {
        if (chatWarmKey) warmTodayChatDrafts('activity-day', JSON.parse(chatWarmKey));
    }, [chatWarmKey]);

    // A row action changed what Go prepares for a row (a follow-up, a
    // reminder, a correction): read the day again.
    const reloadActivities = useCallback(() => {
        void refreshActivitySessionData(`activities:${dateStr}`, loadActivities);
    }, [dateStr, loadActivities]);

    // Meetings retain the same lookback and local-day filtering.
    const loadMeetings = useCallback(async () => {
        const todayStart = dayStartMs(new Date());
        if (dayStart > todayStart) return [];
        const days = Math.round((todayStart - dayStart) / 86400000) + 1;
        const rows = await GetRecentMeetingNotes(days);
        return (rows ?? []).filter((meeting) => meeting.startTime >= dayStart && meeting.startTime < dayEnd);
    }, [dayStart, dayEnd]);
    const meetings = useActivitySessionQuery(`meetings:${dateStr}`, loadMeetings) ?? NO_MEETINGS;

    // ── Digest generation state (backend lifecycle events are the
    //    authoritative signal; the RPC await alone can hang under hot
    //    reload — same reasoning as the old Calendar page) ──
    const [generatingDates, setGeneratingDates] = useState<ReadonlySet<string>>(() => new Set());
    useEffect(() => {
        const onGenerating = (eventDate: unknown) => {
            if (typeof eventDate === 'string') setGeneratingDates((prev) => addDate(prev, eventDate));
        };
        const onDone = (eventDate: unknown) => {
            if (typeof eventDate === 'string') setGeneratingDates((prev) => removeDate(prev, eventDate));
        };
        const c1 = EventsOn('activity:summary-generating', onGenerating);
        const c2 = EventsOn('activity:summary-ready', onDone);
        const c3 = EventsOn('activity:summary-error', onDone);
        return () => {
            c1();
            c2();
            c3();
        };
    }, []);
    const generating = generatingDates.has(dateStr);

    const handleGenerateDigest = useCallback(async () => {
        // Optimistically mark in-progress so the UI updates instantly, before
        // the backend's summary-generating event arrives. The event listeners
        // and the timeout fallback clear it — not the RPC await alone.
        setGeneratingDates((prev) => addDate(prev, dateStr));
        // Safety net for the case where neither the RPC nor any backend event
        // ever lands (process killed mid-flight, IPC torn down). The backend
        // caps the LLM call at 120s; 240s covers that plus IPC slack.
        const timeoutId = window.setTimeout(() => {
            setGeneratingDates((prev) => removeDate(prev, dateStr));
        }, 240_000);
        try {
            await GenerateDailySummary(dateStr);
            // Explicit refetch: the summary-ready event can be missed even
            // when the summary itself updates.
            await reloadDailySummary();
        } catch (err) {
            toast.error(err instanceof Error ? err.message : String(err));
        } finally {
            window.clearTimeout(timeoutId);
            setGeneratingDates((prev) => removeDate(prev, dateStr));
        }
    }, [dateStr, reloadDailySummary]);

    // ── The merged "What you worked on" list, newest first ──
    const workItems = useMemo<WorkItem[]>(() => {
        const out: WorkItem[] = [];
        // A thread with two or more moments on screen is one row; a one-moment
        // thread stays the plain moment row it was before threads.
        const byId = new Map(activities.map((a) => [a.id, a] as const));
        const grouped = new Set<string>();
        for (const thread of threads) {
            const moments = thread.activityIds
                .map((id) => byId.get(id))
                .filter((a): a is ActivityView => a !== undefined);
            if (moments.length < 2) continue;
            for (const moment of moments) grouped.add(moment.id);
            // Newest first: a thread sits where its latest moment would.
            const latest = Math.max(...moments.map((m) => m.startMs));
            out.push({ kind: 'thread', id: `thread-${thread.id}`, startMs: latest, thread, moments });
        }
        for (const activity of activities) {
            if (grouped.has(activity.id)) continue;
            out.push({ kind: 'activity', id: `activity-${activity.id}`, startMs: activity.startMs, activity });
        }
        for (const meeting of meetings) {
            out.push({ kind: 'meeting', id: `meeting-${meeting.meetingKey}`, startMs: meeting.startTime, meeting });
        }
        for (const run of automationRuns) {
            out.push({ kind: 'run', id: `run-${run.id}`, startMs: run.startedAt, run });
        }
        out.sort((a, b) => b.startMs - a.startMs);
        return out;
    }, [activities, threads, meetings, automationRuns]);

    const learnedEntries = useMemo<LearnedEntry[]>(() => {
        const out: LearnedEntry[] = [];
        for (const entry of knowledge) {
            out.push({
                id: `knowledge-${entry.id}`,
                kind: 'knowledge',
                title: entry.title || t('day.untitled'),
                content: entry.content ?? '',
                timestamp: entry.date ? new Date(entry.date).getTime() : 0,
            });
        }
        for (const entry of memoryEntries) {
            out.push({
                id: `memory-${entry.id}`,
                kind: 'memory',
                title: entry.title || t('day.untitled'),
                content: entry.content ?? '',
                timestamp: entry.modTime || 0,
            });
        }
        out.sort((a, b) => b.timestamp - a.timestamp);
        return out;
    }, [knowledge, memoryEntries, t]);

    const query = searchQuery.trim().toLowerCase();

    // ── Stitched by work ──
    const [ownView, setOwnView] = useState<WorkedOnView>('stitched');
    const view = workedOnView ?? ownView;
    const setView = useCallback(
        (next: WorkedOnView) => {
            setOwnView(next);
            onWorkedOnViewChange?.(next);
        },
        [onWorkedOnViewChange],
    );
    useEffect(() => {
        if (!isToday || activityState.loading || threadState.loading) return;
        const previous = new Date(date);
        previous.setDate(previous.getDate() - 1);
        const key = formatDateStr(previous);
        const start = dayStartMs(previous);
        const end = dayEndMs(previous);
        void prefetchActivitySessionData(`activities:${key}`, () => GetActivitiesInRange(start, end));
        void prefetchActivitySessionData(`threads:${key}`, () => GetDayThreads(start, end));
    }, [isToday, dateStr, activityState.loading, threadState.loading]);
    const [now, setNow] = useState(() => Date.now());
    useEffect(() => {
        // Recent work can still qualify just after midnight on the previous day.
        setNow(Date.now());
    }, [dateStr]);
    const stitched = useMemo(
        () => stitchThreads(rawThreads, rawActivities, now, isToday, dayStart, dayEnd),
        [rawThreads, rawActivities, now, isToday, dayStart, dayEnd],
    );
    const visibleStitched = useMemo(
        () =>
            query
                ? stitched.filter(
                      (item) =>
                          item.thread.title.toLowerCase().includes(query) ||
                          item.moments.some((moment) => activityMatchesQuery(moment, query)),
                  )
                : stitched,
        [stitched, query],
    );
    const stitchedStatus =
        threadsFailed || activitiesFailed
            ? 'failed'
            : threadState.loading || activityState.loading
              ? 'loading'
              : threadState.data?.grouped !== true && activities.length > 0
                ? 'linking'
                : 'ready';
    const [selectedThreadId, setSelectedThreadId] = useState<string | null>(null);
    const [scrollThreadId, setScrollThreadId] = useState<string | null>(null);
    const clearScrollThread = useCallback(() => setScrollThreadId(null), []);
    const [, setLaterRevision] = useState(0);
    const candidatePickUp = useMemo(() => pickUpNotice(stitched, dateStr, now), [stitched, dateStr, now]);
    const pickUp = useStablePickUp(candidatePickUp && !isPickUpLater(candidatePickUp.key) ? candidatePickUp : null);
    useEffect(() => {
        // The minute tick extends live stretches and expires the pick-up
        // notice. With neither live work nor a pick-up on screen, `now`
        // stays frozen and the stitched model keeps its cache.
        // It stops while the window is hidden and catches up when shown.
        if (!(isToday && hasLiveActivity) && !candidatePickUp) return;
        return activeInterval(() => setNow(Date.now()), 60_000);
    }, [isToday, hasLiveActivity, candidatePickUp]);
    const showPickUp = useCallback(
        (notice: PickUpNotice) => {
            setView('stitched');
            onSectionChange?.('workedOn');
            setSelectedThreadId(notice.threadId);
            setScrollThreadId(notice.threadId);
        },
        [setView, onSectionChange],
    );
    const pickUpLater = useCallback((notice: PickUpNotice) => {
        setPickUpLater(notice.key);
        setLaterRevision((n) => n + 1);
    }, []);
    // A card's "Open thread" lands here. Wait for the thread rows, then select
    // and scroll to the exact thread once, so a still-loading day never drops
    // the scroll request.
    useEffect(() => {
        if (!initialThreadId || !threadsLoaded) return;
        const thread = workItems.find((item) => item.kind === 'thread' && item.thread.id === initialThreadId);
        if (!thread) {
            onInitialThreadConsumed?.(initialThreadId);
            return;
        }
        setView('stitched');
        setSelectedThreadId(initialThreadId);
        setScrollThreadId(initialThreadId);
        onInitialThreadConsumed?.(initialThreadId);
    }, [initialThreadId, threadsLoaded, workItems, setView, onInitialThreadConsumed]);
    const retryDay = useCallback(() => invalidateActivitySessionCache(), []);
    const viewIds = useId();

    const visibleWorkItems = useMemo(
        () => (query ? workItems.filter((item) => workItemMatchesQuery(item, query)) : workItems),
        [workItems, query],
    );
    const visibleLearned = useMemo(
        () =>
            query
                ? learnedEntries.filter(
                      (entry) =>
                          entry.title.toLowerCase().includes(query) || entry.content.toLowerCase().includes(query),
                  )
                : learnedEntries,
        [learnedEntries, query],
    );

    // The section panel labels two sections with their row counts; report the
    // unfiltered totals so a search query never shrinks them.
    const stitchedRowModel = useMemo(() => stitchedRows(stitched, pickUp?.threadId), [stitched, pickUp?.threadId]);
    const stitchedHead = useMemo(
        () => (stitchedStatus === 'ready' ? stitchedHeadline(t, visibleStitched, pickUp?.threadId) : null),
        [t, stitchedStatus, visibleStitched, pickUp?.threadId],
    );
    const visibleMomentCount = useMemo(() => new Set(visibleStitched.flatMap((item) => item.moments.map((moment) => moment.id))).size, [visibleStitched]);
    const stitchedSub = useMemo(
        // One active time per day (activity review v1.0.0 #5): the Your day
        // number, unless a search narrows the rows.
        () => (stitchedHead ? stitchedSubline(t, visibleStitched, visibleMomentCount, query || !dailyData?.totalActiveMs ? undefined : dailyData.totalActiveMs) : null),
        [t, stitchedHead, visibleStitched, visibleMomentCount, query, dailyData?.totalActiveMs],
    );
    const workedOnCount = view === 'stitched' ? stitchedRowModel.threads.length : workItems.length;
    useEffect(() => {
        onCountsChange?.({ workedOn: workedOnCount, learned: learnedEntries.length });
    }, [onCountsChange, workedOnCount, learnedEntries.length]);

    // One accordion row open at a time across both lists.
    const [openItemId, setOpenItemId] = useState<string | null>(null);
    const handleToggleItem = useCallback((itemId: string) => {
        setOpenItemId((current) => (current === itemId ? null : itemId));
    }, []);
    // A moment inside a thread to open (a suggestion's "From ..." link).
    const [focusMomentId, setFocusMomentId] = useState<string | null>(null);
    const [pendingMomentId, setPendingMomentId] = useState<string | null>(null);
    const [scrollMomentId, setScrollMomentId] = useState<string | null>(null);
    const handleMomentFocusConsumed = useCallback((momentId: string) => {
        setFocusMomentId((current) => current === momentId ? null : current);
    }, []);
    useEffect(() => {
        setOpenItemId(null);
        setPendingMomentId(null);
        setFocusMomentId(null);
        setScrollMomentId(null);
        setSelectedThreadId(null);
        setScrollThreadId(null);
    }, [dateStr]);
    useEffect(() => {
        if (!pendingMomentId || !threadsLoaded) return;
        const direct = workItems.find((w) => w.kind === 'activity' && w.activity.id === pendingMomentId);
        const thread = workItems.find((w) => w.kind === 'thread' && w.moments.some((m) => m.id === pendingMomentId));
        if (!direct && !thread) return;
        setOpenItemId((direct ?? thread)!.id);
        setFocusMomentId(thread ? pendingMomentId : null);
        setScrollMomentId(pendingMomentId);
        setPendingMomentId(null);
    }, [pendingMomentId, workItems, threadsLoaded]);
    useEffect(() => {
        if (!scrollMomentId) return;
        const frame = requestAnimationFrame(() => {
            document.getElementById(`moment-${scrollMomentId}`)?.scrollIntoView({ block: 'nearest' });
            setScrollMomentId(null);
        });
        return () => cancelAnimationFrame(frame);
    }, [scrollMomentId, dateStr]);
    const [lightbox, setLightbox] = useState<{ frames: ActivityRunFrame[]; index: number } | null>(null);
    const openFrame = useCallback((frames: ActivityRunFrame[], index: number) => setLightbox({ frames, index }), []);
    const handleMeetingDeleted = useCallback((meetingKey: string) => {
        updateActivitySessionData<MeetingNotesView[]>(`meetings:${dateStr}`, (current) =>
            (current ?? []).filter((m) => m.meetingKey !== meetingKey),
        );
    }, [dateStr]);

    // ── Derived digest numbers ──
    const displaySummary = cleanSummaryText(dailyData?.aiSummary ?? '');
    const activeMs = dailyData?.totalActiveMs ?? 0;
    const topTwoApps = (dailyData?.appBreakdown ?? []).slice(0, 2);
    const appsText =
        topTwoApps.length === 0
            ? null
            : topTwoApps.length === 1
              ? topTwoApps[0].appName
              : t('day.appsJoin', { first: topTwoApps[0].appName, second: topTwoApps[1].appName });
    const isFuture = dayStart > dayStartMs(new Date());
    const nothingToShow =
        !dayLoading &&
        !displaySummary &&
        activeMs === 0 &&
        workItems.length === 0 &&
        learnedEntries.length === 0 &&
        !activitiesFailed &&
        !threadsFailed &&
        !generating;

    const yourDaySection = (
        <>
            <section>
                <h3 className={sectionLabelClass}>{t('day.yourDay')}</h3>
                <AISummaryCard
                    isToday={isToday}
                    summary={dailyData?.aiSummary ?? ''}
                    summaryStatus={dailyData?.aiSummaryStatus ?? 'none'}
                    cachedLanguage={dailyData?.aiSummaryLanguage}
                    generating={generating}
                    autoStatus={dailyData?.autoStatus}
                    onGenerate={handleGenerateDigest}
                    onAskAI={onAskAboutDate ? () => onAskAboutDate(dateStr, 'daily') : undefined}
                    scope="daily"
                    isPartial={dailyData?.isPartial}
                    summaryCreatedAt={dailyData?.summaryCreatedAt}
                    emptyActionLabel={isFuture ? undefined : t('day.writeSummary')}
                >
                    {activeMs > 0 && (
                        <div className="border-t border-border/60 pt-3 space-y-3">
                            <p className="text-[12px] text-muted">
                                {appsText
                                    ? t('day.activeLineWithApps', {
                                          duration: formatDurationMs(activeMs),
                                          apps: appsText,
                                      })
                                    : t('day.activeLineNoApps', { duration: formatDurationMs(activeMs) })}
                            </p>
                            <DayDetails
                                hourly={dailyData?.hourlyActive ?? []}
                                appBreakdown={dailyData?.appBreakdown ?? []}
                                hourRange={hourRange}
                                maxHourly={maxHourly}
                            />
                        </div>
                    )}
                </AISummaryCard>
            </section>
            <ActivityNoticed
                key={dateStr}
                dateStr={dateStr}
                focus={noticedFocus}
                onFocusConsumed={onNoticedFocusConsumed}
                onOpenMoment={(momentId) => {
                    // A moment opens in the list as it was recorded.
                    setView('recorded');
                    setPendingMomentId(momentId);
                    onSectionChange?.('workedOn');
                }}
                pickUp={pickUp}
                onPickUpShow={showPickUp}
                onPickUpLater={pickUpLater}
            />
        </>
    );

    // The Your day fallback (while the digest loads) shows the list as
    // recorded, as before, without the switch.
    const renderWorkedOn = (mode: WorkedOnView, withSwitch: boolean) => {
        const headline =
            mode === 'stitched'
                ? stitchedHead
                : workItems.length > 0
                  ? t('stitched.recordedHeadline', { count: workItems.length })
                  : null;
        const subline = headline ? (mode === 'stitched' ? stitchedSub : t('stitched.recordedSubline')) : null;
        return (
            <section>
                <div className={cn('mb-4 flex flex-wrap gap-x-6 gap-y-3', headline ? 'items-end' : 'items-center')}>
                    <div className="flex min-w-0 flex-[1_1_320px] flex-col gap-1">
                        <h3 className={cn(sectionLabelClass, 'mb-0')}>{t('day.workedOn')}</h3>
                        {headline && <p className="display px-1 text-ink">{headline}</p>}
                        {subline && <p className="px-1 text-[14px] text-muted">{subline}</p>}
                        {isToday && <ActivityCaptureLine />}
                    </div>
                    {withSwitch && (
                    <div
                        role="tablist"
                        aria-label={t('stitched.viewLabel')}
                        className="flex shrink-0 gap-0.5 rounded-[10px] border border-border bg-panel-warm p-[3px]"
                    >
                        {WORKED_ON_VIEWS.map((tab) => (
                            <button
                                key={tab}
                                id={`${viewIds}-tab-${tab}`}
                                type="button"
                                role="tab"
                                data-worked-on-view={tab}
                                aria-selected={view === tab}
                                aria-controls={`${viewIds}-panel`}
                                tabIndex={view === tab ? 0 : -1}
                                onClick={() => setView(tab)}
                                onKeyDown={(e) => {
                                    // Arrow keys move between the two views.
                                    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
                                    e.preventDefault();
                                    const next = tab === 'stitched' ? 'recorded' : 'stitched';
                                    setView(next);
                                    document.getElementById(`${viewIds}-tab-${next}`)?.focus();
                                }}
                                className={cn(
                                    'h-[30px] rounded-lg px-3 text-[13px] font-medium text-ink transition-colors duration-[120ms]',
                                    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus',
                                    view === tab ? 'bg-surface-active' : 'hover:bg-surface-hover',
                                )}
                            >
                                {tab === 'stitched' ? t('stitched.stitchedTab') : t('stitched.recordedTab')}
                            </button>
                        ))}
                    </div>
                    )}
                </div>
                <div
                    role={withSwitch ? 'tabpanel' : undefined}
                    id={withSwitch ? `${viewIds}-panel` : undefined}
                    aria-labelledby={withSwitch ? `${viewIds}-tab-${view}` : undefined}
                >
                    {mode === 'stitched' ? (
                        <ActivityStitchedView
                            items={visibleStitched}
                            resumeThreadId={pickUp?.threadId}
                            threads={threads}
                            status={stitchedStatus}
                            query={searchQuery.trim()}
                            selectedThreadId={selectedThreadId}
                            onSelectThread={setSelectedThreadId}
                            scrollThreadId={scrollThreadId}
                            onScrolled={clearScrollThread}
                            // Stacked (no section panel) the pick-up already shows
                            // under Isoform noticed.
                            pickUp={
                                section && pickUp && visibleStitched.some((item) => item.thread.id === pickUp.threadId)
                                    ? pickUp
                                    : null
                            }
                            onPickUpShow={showPickUp}
                            onPickUpLater={pickUpLater}
                            onRetry={retryDay}
                            empty={isToday && firstDay.firstDays ? <FirstDayNowRow /> : undefined}
                        />
                    ) : (
                        <>
                            {activitiesFailed && (
                                <div className="mb-3">
                                    <StitchedFailure onRetry={retryDay} />
                                </div>
                            )}
                            {visibleWorkItems.length > 0 ? (
                                <ul className="divide-y divide-border/60 rounded-xl border border-border bg-panel overflow-hidden">
                                    {visibleWorkItems.map((item) => (
                                        <WorkItemRow
                                            key={item.id}
                                            item={item}
                                            threads={threads}
                                            focusMomentId={focusMomentId}
                                            onFocusMomentConsumed={handleMomentFocusConsumed}
                                            open={openItemId === item.id || (item.kind === 'thread' && !!query)}
                                            onToggleItem={handleToggleItem}
                                            bridgePort={bridgePort}
                                            onOpenFrame={openFrame}
                                            onOpenRunDetail={onOpenRunDetail}
                                            onMeetingDeleted={handleMeetingDeleted}
                                            onActivityDeleted={handleActivityDeleted}
                                            onActivityChanged={reloadActivities}
                                            onStartChat={onStartChat}
                                        />
                                    ))}
                                </ul>
                            ) : activitiesFailed ? null : (
                                !query && isToday ? <ActivityTodaySoon /> : (
                                <p className="text-[12px] text-muted/70 px-1">
                                    {/* Today, empty means "not yet" (first run 1.0): say when it fills. */}
                                    {query ? t('day.noMatches', { query: searchQuery.trim() }) : isToday ? t('day.workedOnSoon') : t('day.sectionEmpty')}
                                </p>
                                )
                            )}
                        </>
                    )}
                </div>
            </section>
        );
    };
    const workedOnSection = renderWorkedOn(view, true);

    const learnedSection = (
        <section>
            <h3 className={sectionLabelClass}>{t('day.learned')}</h3>
            {visibleLearned.length > 0 ? (
                <ul className="divide-y divide-border/60 rounded-xl border border-border bg-panel overflow-hidden">
                    {visibleLearned.map((entry) => (
                        <LearnedRow
                            key={entry.id}
                            entry={entry}
                            open={openItemId === entry.id}
                            onToggleItem={handleToggleItem}
                            onOpenSession={onOpenSession}
                        />
                    ))}
                </ul>
            ) : (
                <p className="text-[12px] text-muted/70 px-1">
                    {query ? t('day.noMatches', { query: searchQuery.trim() }) : t('day.sectionEmpty')}
                </p>
            )}
        </section>
    );

    return (
        // The page remounts on every open; the day's scroll offset survives in
        // the facade, and the warm cache makes the height right on the first
        // commit of a revisit.
        <div
            ref={scrollRef}
            className="flex-1 overflow-y-auto"
            onScroll={(event) => rememberActivityScroll(`day:${dateStr}`, event.currentTarget.scrollTop)}
        >
            {/* What you worked on is wider so the timeline holds at 1280 px. */}
            <div className={cn('mx-auto px-6 py-6 space-y-6', section === 'workedOn' ? 'max-w-4xl' : 'max-w-2xl')}>
                {section ? (
                    <>
                        {/* One section at a time (the Activity page's panel). */}
                        {section === 'yourDay' && (dailyData || !dayLoading ? yourDaySection : renderWorkedOn('recorded', false))}
                        {section === 'workedOn' && workedOnSection}
                        {section === 'learned' && learnedEntries.length > 0 && learnedSection}
                    </>
                ) : nothingToShow ? (
                    <p className="py-16 text-center text-[13px] text-muted/70">{t('day.empty')}</p>
                ) : (
                    <>
                        {(dailyData || !dayLoading) && yourDaySection}
                        {(workItems.length > 0 || query || activitiesFailed || threadsFailed) && workedOnSection}
                        {(learnedEntries.length > 0 || query) && learnedSection}
                    </>
                )}
            </div>
            {lightbox && (
                <FrameLightbox
                    frames={lightbox.frames}
                    index={lightbox.index}
                    highlightKeywords={[]}
                    matchedPaths={[]}
                    bridgePort={bridgePort}
                    onClose={() => setLightbox(null)}
                />
            )}
        </div>
    );
}

// ── "What you worked on" rows ──

type WorkItem =
    | { kind: 'activity'; id: string; startMs: number; activity: ActivityView }
    | { kind: 'thread'; id: string; startMs: number; thread: ActivityThreadView; moments: ActivityView[] }
    | { kind: 'meeting'; id: string; startMs: number; meeting: MeetingNotesView }
    | { kind: 'run'; id: string; startMs: number; run: CronRunWithJobName };

function activityMatchesQuery(a: ActivityView, query: string): boolean {
    return (
        a.title.toLowerCase().includes(query) ||
        a.description.toLowerCase().includes(query) ||
        a.latestChange.toLowerCase().includes(query) ||
        a.apps.some((app) => app.appName.toLowerCase().includes(query)) ||
        a.entities.some((e) => e.name.toLowerCase().includes(query))
    );
}

function workItemMatchesQuery(item: WorkItem, query: string): boolean {
    switch (item.kind) {
        case 'activity':
            return activityMatchesQuery(item.activity, query);
        case 'thread':
            return (
                item.thread.title.toLowerCase().includes(query) ||
                item.moments.some((moment) => activityMatchesQuery(moment, query))
            );
        case 'meeting': {
            const m = item.meeting;
            return (
                m.title.toLowerCase().includes(query) ||
                m.meetingApp.toLowerCase().includes(query) ||
                m.summary.toLowerCase().includes(query)
            );
        }
        case 'run':
            return item.run.jobName.toLowerCase().includes(query);
    }
}

interface WorkItemRowProps {
    item: WorkItem;
    threads: ActivityThreadView[];
    focusMomentId?: string | null;
    onFocusMomentConsumed?: (momentId: string) => void;
    open: boolean;
    onToggleItem: (itemId: string) => void;
    bridgePort: number;
    onOpenFrame: (frames: ActivityRunFrame[], index: number) => void;
    onOpenRunDetail: (jobId: string) => void;
    onMeetingDeleted: (meetingKey: string) => void;
    onActivityDeleted: (activityId: string) => void;
    onActivityChanged: () => void;
    onStartChat?: (prompt: string, context: TodayChatContextRef) => void;
}

const WorkItemRow = memo(function WorkItemRow({
    item,
    threads,
    open,
    onToggleItem,
    bridgePort,
    onOpenFrame,
    onOpenRunDetail,
    onMeetingDeleted,
    onActivityDeleted,
    onActivityChanged,
    onStartChat,
    focusMomentId,
    onFocusMomentConsumed,
}: WorkItemRowProps) {
    const onToggle = useCallback(() => onToggleItem(item.id), [onToggleItem, item.id]);
    const moveControl = (activityId: string, currentThreadId: string | null): ReactNode => (
        <ThreadMoveControl activityId={activityId} currentThreadId={currentThreadId} threads={threads} />
    );
    switch (item.kind) {
        case 'activity':
            return (
                <ActivityWorkRow
                    activity={item.activity}
                    open={open}
                    onToggle={onToggle}
                    bridgePort={bridgePort}
                    onOpenFrame={onOpenFrame}
                    onDeleted={onActivityDeleted}
                    onChanged={onActivityChanged}
                    onStartChat={onStartChat}
                    threadControl={moveControl(
                        item.activity.id,
                        threads.find((thread) => thread.activityIds.includes(item.activity.id))?.id ?? null,
                    )}
                />
            );
        case 'thread':
            return (
                <ThreadWorkRow
                    thread={item.thread}
                    moments={item.moments}
                    open={open}
                    onToggle={onToggle}
                    focusMomentId={focusMomentId}
                    onFocusMomentConsumed={onFocusMomentConsumed}
                    renderMoment={(moment, momentOpen, onMomentToggle) => (
                        <ActivityWorkRow
                            key={moment.id}
                            activity={moment}
                            open={momentOpen}
                            onToggle={onMomentToggle}
                            bridgePort={bridgePort}
                            onOpenFrame={onOpenFrame}
                            onDeleted={onActivityDeleted}
                            onChanged={onActivityChanged}
                            onStartChat={onStartChat}
                            threadControl={moveControl(moment.id, item.thread.id)}
                        />
                    )}
                />
            );
        case 'meeting':
            return (
                <MeetingWorkRow
                    meeting={item.meeting}
                    open={open}
                    onToggle={onToggle}
                    bridgePort={bridgePort}
                    onDeleted={() => onMeetingDeleted(item.meeting.meetingKey)}
                />
            );
        case 'run':
            return <RunWorkRow run={item.run} onOpenRunDetail={onOpenRunDetail} />;
    }
// Handler props are excluded from the comparison: they dispatch to stable
// setters or day-scoped callbacks, and the day they close over is also what
// `item`/`threads` identity tracks — a new day re-renders every row anyway.
}, (prev, next) =>
    prev.item === next.item &&
    prev.threads === next.threads &&
    prev.open === next.open &&
    prev.focusMomentId === next.focusMomentId &&
    prev.bridgePort === next.bridgePort &&
    prev.onFocusMomentConsumed === next.onFocusMomentConsumed);

const rowTimeClass = 'w-14 shrink-0 text-[11px] text-muted/80 tabular-nums pt-[2px]';

/** A quiet marker for an activity with recent evidence still in progress. */
function LiveDot({ className }: { className?: string }) {
    return <span className={cn('h-1.5 w-1.5 shrink-0 rounded-full bg-accent-2', className)} />;
}

const OUTCOME_STATUS_KEYS = {
    reported: 'activityCard.outcome.reported',
    fixed: 'activityCard.outcome.fixed',
    done: 'activityCard.outcome.done',
    abandoned: 'activityCard.outcome.abandoned',
} as const;

const OUTCOME_REASON_KEYS = {
    message_sent: 'activityCard.outcomeReason.messageSent',
    target_changed: 'activityCard.outcomeReason.targetChanged',
    user_completed: 'activityCard.outcomeReason.userCompleted',
    completion_click: 'activityCard.outcomeReason.completionClick',
    quiet_timeout: 'activityCard.outcomeReason.quietTimeout',
} as const;

/** The outcome the old activity card showed: "Reported to X at 3:12 PM",
 *  Fixed, Done or Abandoned, with its evidence reason. Same data, same copy. */
function ActivityOutcomeLine({ outcome }: { outcome: ActivityOutcome }) {
    const { t, i18n } = useTranslation('activity');
    const time = new Date(outcome.occurredAt).toLocaleTimeString(i18n.language, { hour: '2-digit', minute: '2-digit' });
    const status = outcome.status === 'reported' && outcome.target
        ? t('activityCard.outcome.reportedTo', { target: outcome.target, time })
        : t(OUTCOME_STATUS_KEYS[outcome.status], { time });
    const reasonKey = OUTCOME_REASON_KEYS[outcome.reason as keyof typeof OUTCOME_REASON_KEYS]
        ?? 'activityCard.outcomeReason.recordedEvidence';
    return (
        <p className="text-[12px] leading-relaxed text-muted">
            {t('activityCard.statusWithReason', { status, reason: t(reasonKey) })}
        </p>
    );
}

function ActivityWorkRow({
    activity,
    open,
    onToggle,
    bridgePort,
    onOpenFrame,
    onDeleted,
    onChanged,
    threadControl,
    onStartChat,
}: {
    activity: ActivityView;
    open: boolean;
    onToggle: () => void;
    bridgePort: number;
    onOpenFrame: (frames: ActivityRunFrame[], index: number) => void;
    onDeleted: (activityId: string) => void;
    onChanged: () => void;
    /** Not right on a moment: move it to another thread or split it off. */
    threadControl?: ReactNode;
    onStartChat?: (prompt: string, context: TodayChatContextRef) => void;
}) {
    const { t } = useTranslation('activity');
    const { t: tToday, i18n } = useTranslation('today');
    useAppIcons(activity.apps.map((a) => a.appKey));
    // The words Today would write for this moment; Go's prepared draft
    // replaces them while untouched, and Send reads the moment again.
    const chatAbout = onStartChat
        ? () =>
              onStartChat(
                  formatTodayChatPrefill(
                      {
                          kind: 'activity',
                          title: activity.title,
                          occurredAt: activity.endMs,
                          durationMinutes: Math.round(activity.activeMs / 60_000),
                      },
                      tToday,
                      i18n.resolvedLanguage ?? i18n.language,
                  ),
                  { sourceKind: 'activity', sourceId: activity.id },
              )
        : undefined;
    // The same two-step confirm the old activity card had: the action turns
    // into Delete / Cancel in place.
    const [confirmingDelete, setConfirmingDelete] = useState(false);
    const doDelete = () => {
        DeleteActivity(activity.id)
            .then(() => onDeleted(activity.id))
            .catch((err) => console.error('[activity] delete activity failed:', err));
    };
    const oneLine = activity.description || activity.latestChange || '';
    // People and the real repos/projects/pull-requests/channels this moment
    // touched. Topics stay out: they were the keyword chips, which the Day
    // view drops.
    const people = activity.entities.filter((e) => e.type === 'person').map((e) => e.name);
    const links = activity.entities
        .filter((e) => e.type !== 'person' && e.type !== 'topic')
        .map((e) => e.name);
    return (
        <li id={`moment-${activity.id}`}>
            <button
                type="button"
                onClick={onToggle}
                aria-expanded={open}
                className="w-full flex items-start gap-3 px-4 py-3 text-left hover:bg-surface-hover transition-colors"
            >
                <time className={rowTimeClass}>{formatTimeFromMs(activity.startMs)}</time>
                <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                        <span className="text-[13px] font-medium text-ink truncate">{activity.title}</span>
                        {activity.inProgress && (
                            <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-border px-2 py-0.5 text-[10px] font-medium text-muted">
                                <LiveDot />
                                {t('activityCard.live')}
                            </span>
                        )}
                    </div>
                    {oneLine && <p className="text-[12px] text-muted truncate mt-0.5">{oneLine}</p>}
                </div>
                {activity.apps.length > 0 && (
                    <span className="flex items-center shrink-0 pt-0.5">
                        {activity.apps.slice(0, 4).map((app, i) => (
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
                <div className="px-4 pb-4 pt-1 space-y-3">
                    {activity.description && (
                        <p className="text-[13px] text-ink/80 leading-relaxed">{activity.description}</p>
                    )}
                    {activity.latestChange && activity.latestChange !== activity.description && (
                        <p className="text-[12px] text-muted leading-relaxed">{activity.latestChange}</p>
                    )}
                    {activity.latestOutcome && <ActivityOutcomeLine outcome={activity.latestOutcome} />}
                    {people.length > 0 && (
                        <p className="text-[12px] text-muted">{t('day.people', { names: people.join(', ') })}</p>
                    )}
                    {links.length > 0 && (
                        <p className="text-[12px] text-muted">{t('day.links', { names: links.join(', ') })}</p>
                    )}
                    {activity.thumbnails.length > 0 && bridgePort > 0 && (
                        <div className="flex flex-wrap gap-2">
                            {activity.thumbnails.map((frame, i) => (
                                <RunFrameThumb
                                    key={`${frame.path}-${i}`}
                                    frame={frame}
                                    bridgePort={bridgePort}
                                    alt={activity.title}
                                    onClick={() => onOpenFrame(activity.thumbnails, i)}
                                />
                            ))}
                        </div>
                    )}
                    <ActivityRowActions
                        activity={activity}
                        onChanged={onChanged}
                        threadControl={threadControl}
                        onChatAbout={chatAbout}
                        deleteControl={
                            confirmingDelete ? (
                                <div className="flex items-center gap-1">
                                    <button
                                        type="button"
                                        onClick={doDelete}
                                        className="text-[11px] px-1.5 py-0.5 rounded bg-panel-warm text-error hover:bg-panel-warm"
                                    >
                                        {t('activityCard.confirmDelete')}
                                    </button>
                                    <button
                                        type="button"
                                        onClick={() => setConfirmingDelete(false)}
                                        className="text-[11px] px-1.5 py-0.5 rounded text-muted hover:text-ink"
                                    >
                                        {t('activityCard.cancel')}
                                    </button>
                                </div>
                            ) : (
                                <button
                                    type="button"
                                    onClick={() => setConfirmingDelete(true)}
                                    className="inline-flex items-center gap-1 text-[11px] text-muted hover:text-error transition-colors"
                                >
                                    <Trash2 className="h-3 w-3" />
                                    {t('day.deleteFromHistory')}
                                </button>
                            )
                        }
                    />
                </div>
            )}
        </li>
    );
}

function MeetingWorkRow({
    meeting,
    open,
    onToggle,
    bridgePort,
    onDeleted,
}: {
    meeting: MeetingNotesView;
    open: boolean;
    onToggle: () => void;
    bridgePort: number;
    onDeleted: () => void;
}) {
    const { t } = useTranslation('activity');
    const title =
        meeting.title ||
        (meeting.meetingApp ? t('meetingNotes.callTitle', { app: meeting.meetingApp }) : t('meetingNotes.untitledCall'));
    const oneLine = meeting.summary.split('\n')[0]?.trim() ?? '';
    return (
        <li>
            <button
                type="button"
                onClick={onToggle}
                aria-expanded={open}
                className="w-full flex items-start gap-3 px-4 py-3 text-left hover:bg-surface-hover transition-colors"
            >
                <time className={rowTimeClass}>{formatTimeFromMs(meeting.startTime)}</time>
                <div className="flex-1 min-w-0">
                    <div className="text-[13px] font-medium text-ink truncate">{title}</div>
                    {oneLine && <p className="text-[12px] text-muted truncate mt-0.5">{oneLine}</p>}
                </div>
                {meeting.meetingApp && (
                    <span className="shrink-0 pt-0.5" title={meeting.meetingApp}>
                        <AppGlyph appKey="" appName={meeting.meetingApp} className="h-4 w-4" />
                    </span>
                )}
                <ChevronDown
                    className={`h-3.5 w-3.5 shrink-0 mt-1 text-muted/60 transition-transform ${open ? 'rotate-180' : ''}`}
                />
            </button>
            {open && (
                <div className="px-4 pb-4 pt-1">
                    <MeetingNotesCard meeting={meeting} bridgePort={bridgePort} onDeleted={onDeleted} />
                </div>
            )}
        </li>
    );
}

function RunWorkRow({ run, onOpenRunDetail }: { run: CronRunWithJobName; onOpenRunDetail: (jobId: string) => void }) {
    const { t } = useTranslation('activity');
    const result = displayableRunResult(run.resultSummary);
    const errorText = displayableRunResult(run.error);
    return (
        <li>
            <button
                type="button"
                onClick={() => onOpenRunDetail(run.jobId)}
                className="w-full flex items-start gap-3 px-4 py-3 text-left hover:bg-surface-hover transition-colors"
            >
                <time className={rowTimeClass}>{formatTimeFromMs(run.startedAt)}</time>
                <Zap className="h-3.5 w-3.5 shrink-0 mt-[3px] text-automation" />
                <div className="flex-1 min-w-0">
                    <div className="text-[13px] font-medium text-ink truncate">
                        {t('day.isoformRan', { name: run.jobName || t('day.unknownSchedule') })}
                    </div>
                    {result ? (
                        <p className="text-[12px] text-muted truncate mt-0.5">{result}</p>
                    ) : errorText ? (
                        <p className="text-[12px] text-error truncate mt-0.5">{errorText}</p>
                    ) : null}
                </div>
                {!result && (
                    <span className="shrink-0 text-[11px] text-accent-2 pt-[3px]">{t('day.openResult')}</span>
                )}
            </button>
        </li>
    );
}

// ── "What Isoform learned" rows ──

interface LearnedEntry {
    id: string;
    kind: 'knowledge' | 'memory';
    title: string;
    content: string;
    timestamp: number;
}

const LearnedRow = memo(function LearnedRow({
    entry,
    open,
    onToggleItem,
    onOpenSession,
}: {
    entry: LearnedEntry;
    open: boolean;
    onToggleItem: (itemId: string) => void;
    onOpenSession: (sessionId: string) => void;
}) {
    const { t } = useTranslation('activity');
    const onToggle = useCallback(() => onToggleItem(entry.id), [onToggleItem, entry.id]);
    return (
        <li>
            <button
                type="button"
                onClick={onToggle}
                aria-expanded={open}
                className="w-full flex items-center gap-3 px-4 py-2.5 text-left hover:bg-surface-hover transition-colors"
            >
                <span className="flex-1 min-w-0 text-[13px] text-ink truncate">{entry.title}</span>
                <span className="shrink-0 text-[10px] text-muted/80">
                    {entry.kind === 'knowledge' ? t('day.learnedKnowHow') : t('day.learnedAboutYou')}
                </span>
                <ChevronDown
                    className={`h-3.5 w-3.5 shrink-0 text-muted/60 transition-transform ${open ? 'rotate-180' : ''}`}
                />
            </button>
            {open && (
                <div className="px-4 pb-4 pt-1">
                    {entry.content ? (
                        <EntryDetail
                            content={entry.content}
                            onOpenActivitySession={onOpenSession}
                            cardPadding="px-5 py-4"
                            compactSessions
                            collapsibleSessions
                        />
                    ) : (
                        <p className="text-[13px] text-muted/70 italic">{t('day.noContent')}</p>
                    )}
                </div>
            )}
        </li>
    );
// Same closure exclusion as WorkItemRow: the handlers are day-stable.
}, (prev, next) => prev.entry === next.entry && prev.open === next.open);

// ── Details disclosure: the hour chart and top applications ──

function DayDetails({
    hourly,
    appBreakdown,
    hourRange,
    maxHourly,
}: {
    hourly: number[];
    appBreakdown: main.AppUsageStat[];
    hourRange: { start: number; end: number };
    maxHourly: number;
}) {
    const { t } = useTranslation('activity');
    const [open, setOpen] = useState(false);
    const [hoveredHour, setHoveredHour] = useState<number | null>(null);

    let peakHour = -1;
    let peakMinutes = 0;
    for (let h = 0; h < hourly.length; h++) {
        if ((hourly[h] || 0) > peakMinutes) {
            peakMinutes = hourly[h];
            peakHour = h;
        }
    }
    const formatHourLabel = (h: number) => {
        if (h === 0) return t('day.midnight');
        if (h < 12) return t('day.hourAm', { hour: h });
        if (h === 12) return t('day.noon');
        return t('day.hourPm', { hour: h - 12 });
    };

    return (
        <div>
            <button
                type="button"
                onClick={() => setOpen((o) => !o)}
                aria-expanded={open}
                className="flex items-center gap-1 text-[11px] text-muted hover:text-ink transition-colors"
            >
                {open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
                {t('day.details')}
            </button>
            {open && (
                <div className="mt-3 grid grid-cols-1 lg:grid-cols-[minmax(0,1.2fr)_minmax(220px,0.8fr)] gap-4">
                    <div>
                        <h5 className="text-[10px] uppercase tracking-[0.08em] font-semibold text-muted/80 mb-3">
                            {t('day.byHour')}
                        </h5>
                        {hourly.length === 0 ? (
                            <p className="text-[12px] text-muted/70 py-2">{t('day.noHourly')}</p>
                        ) : (
                            <>
                                <div className="flex items-stretch gap-[3px] h-[72px] relative">
                                    {Array.from({ length: hourRange.end - hourRange.start + 1 }, (_, i) => {
                                        const hour = hourRange.start + i;
                                        const minutes = hourly[hour] || 0;
                                        const pct = maxHourly > 0 ? minutes / maxHourly : 0;
                                        const isPeak = hour === peakHour && minutes > 0;
                                        return (
                                            <div
                                                key={hour}
                                                className="flex-1 flex flex-col items-center justify-end gap-1 relative group cursor-default"
                                                onMouseEnter={() => setHoveredHour(hour)}
                                                onMouseLeave={() => setHoveredHour(null)}
                                            >
                                                <div
                                                    className="w-full rounded-[3px] bg-accent transition-all"
                                                    style={{
                                                        height: `${Math.max(pct * 100, minutes > 0 ? 6 : 0)}%`,
                                                        opacity: minutes > 0 ? (isPeak ? 1 : 0.3 + 0.6 * pct) : 0.06,
                                                    }}
                                                />
                                                {hoveredHour === hour && minutes > 0 && (
                                                    <div className="absolute -top-8 left-1/2 -translate-x-1/2 px-2 py-1 text-[10px] font-medium text-ink bg-panel border border-border rounded-md shadow-sm whitespace-nowrap z-10 tabular-nums">
                                                        {t('day.hourTooltip', { hour: formatHourLabel(hour), minutes })}
                                                    </div>
                                                )}
                                            </div>
                                        );
                                    })}
                                </div>
                                <div className="flex justify-between mt-2">
                                    {Array.from({ length: hourRange.end - hourRange.start + 1 }, (_, i) => {
                                        const hour = hourRange.start + i;
                                        const total = hourRange.end - hourRange.start;
                                        const show = i === 0 || i === total || hour % 3 === 0;
                                        return (
                                            <span
                                                key={hour}
                                                className={`flex-1 text-center text-[9px] tabular-nums ${hour === peakHour ? 'text-accent font-semibold' : 'text-muted/60'}`}
                                            >
                                                {show ? hour : ''}
                                            </span>
                                        );
                                    })}
                                </div>
                            </>
                        )}
                    </div>
                    <div className="border-t border-border/60 lg:border-t-0 lg:border-l lg:pl-4 pt-3 lg:pt-0">
                        <h5 className="text-[10px] uppercase tracking-[0.08em] font-semibold text-muted/80 mb-3">
                            {t('day.topApps')}
                        </h5>
                        <DayTopApplications apps={appBreakdown} />
                    </div>
                </div>
            )}
        </div>
    );
}

function DayTopApplications({ apps }: { apps: main.AppUsageStat[] }) {
    const { t } = useTranslation('activity');
    const topApps = (apps ?? []).slice(0, 4);
    useAppIcons(topApps.map((app) => app.bundleId ?? ''));
    if (topApps.length === 0) {
        return <p className="text-[12px] text-muted/70 py-2">{t('day.noApps')}</p>;
    }
    return (
        <div className="space-y-2">
            {topApps.map((app) => (
                <div key={app.appName} className="flex items-center gap-2.5">
                    <AppGlyph
                        appKey={app.bundleId ?? ''}
                        appName={app.appName}
                        className="size-6 shrink-0 rounded-md text-[10px]"
                    />
                    <div className="min-w-0 flex-1">
                        <div className="flex items-baseline gap-2">
                            <span className="text-[12px] text-ink truncate" title={app.appName}>
                                {app.appName}
                            </span>
                            <span className="ml-auto text-[10px] text-muted/70 tabular-nums">
                                {formatDurationMs(app.activeMs)}
                            </span>
                        </div>
                        <div className="mt-1 h-[5px] bg-panel-warm rounded-full overflow-hidden">
                            <div
                                className="h-full bg-accent rounded-full transition-[width] duration-300"
                                style={{ width: `${Math.max(app.percentage, 2)}%` }}
                            />
                        </div>
                    </div>
                </div>
            ))}
        </div>
    );
}

/** Today with no sessions yet. On the first two days it says when sessions
 *  start and shows what Isoform sees right now as one live row (first-day
 *  handoff §5); otherwise the usual "after about 30 minutes" line. */
function ActivityTodaySoon() {
    const { t } = useTranslation('activity');
    const firstDay = useFirstDay();
    if (!firstDay.firstDays) return <p className="text-[12px] text-muted/70 px-1">{t('day.workedOnSoon')}</p>;
    return <FirstDayNowRow />;
}

function FirstDayNowRow() {
    const { t } = useTranslation('activity');
    const now = useDoingNow();
    const app = now && !now.frontAppHidden ? (now.app || now.frontApp || '') : '';
    if (!app) return <p className="text-[12px] text-muted/70 px-1">{t('day.workedOnSoon')}</p>;
    return (
        <div className="flex flex-col gap-2 px-1" data-testid="activity-first-day">
            <p className="text-[12px] text-muted">{t('firstDay.rightNow')}</p>
            <div className="flex items-center gap-2.5 rounded-xl border border-border bg-panel px-3.5 py-2.5" data-testid="activity-now-row">
                <span className="size-2 shrink-0 rounded-full bg-accent animate-breathe opacity-80 motion-reduce:animate-none" aria-hidden />
                <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{[app, now?.title].filter(Boolean).join(' · ')}</span>
                <span className="shrink-0 text-[12px] font-medium text-accent">{t('firstDay.now')}</span>
            </div>
        </div>
    );
}
