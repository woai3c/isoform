import { markPageContent } from '@/lib/page-switch-timing';
import { useCallback, useEffect, useMemo, useState } from 'react';
import {
    EnsureDailySummary,
    GetActivityMonthStats,
    GetCronRunsByDateRange,
    GetKnowledgeEntries,
    GetMemoryEntries,
} from '../../../wailsjs/go/main/App';
import { ListCronJobs } from '@/lib/page-queries';
import { invalidateActivitySessionCache, useActivitySessionQuery, useActivitySessionQueryState } from './activity-session-cache';
import { useDaySummaryRetry } from './day-summary-retry';
import type { StreamItem } from '@/types';
import type { CronJobState, CronRunState } from '@/hooks/useCronJobs';
import i18n from '@/i18n';

// ── Types ──

export interface DayHeatmapStats {
    activeMs: number;
    sessionCount: number;
    automationCreatedCount: number;
    automationRunCount: number;
    knowledgeCount: number;
    memoryCount: number;
}

export interface CronRunWithJobName {
    id: string;
    jobId: string;
    startedAt: number;
    finishedAt: number;
    status: string;
    triggerKind: string;
    error: string;
    durationMs: number;
    resultSummary: string;
    hasLogs: boolean;
    notifyResult: string;
    jobName: string;
}

// ── Helpers ──

export function formatDateStr(d: Date): string {
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

const isChinese = (language?: string) => (language ?? i18n.language ?? '').startsWith('zh');

function durationWords(totalMin: number): string {
    const t = i18n.getFixedT(null, 'activity');
    if (totalMin < 60) return t('stitched.durationMinutes', { minutes: totalMin });
    const hours = Math.floor(totalMin / 60);
    const minutes = totalMin % 60;
    return minutes > 0 ? t('stitched.durationHoursMinutes', { hours, minutes }) : t('stitched.durationHours', { hours });
}

/** "3h 38m" / "3 小时 38 分钟": whole minutes, rounded down. */
export function formatDurationMs(ms: number): string {
    return durationWords(ms <= 0 ? 0 : Math.floor(ms / 60000));
}

/** A session's length in the Screen timeline list (activity review v1.0.7
 *  AR14): minutes only, rounded, anything under a minute reads "1m". */
export function formatRoundedDurationMs(ms: number): string {
    return durationWords(Math.max(1, Math.round(ms / 60000)));
}

/** "9:12 AM"; in Chinese 24-hour with no leading zero or 上午 (activity review v1.0.6 AR42): "9:12", "13:05". */
export function formatTimeFromMs(ms: number, language?: string): string {
    const d = new Date(ms);
    if (isChinese(language)) return `${d.getHours()}:${String(d.getMinutes()).padStart(2, '0')}`;
    return d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
}

/** "9:12–10:02 AM": no leading zeros or seconds, the shared part said once;
 *  "9:12–10:02" in Chinese. */
export function formatTimeRangeFromMs(startMs: number, endMs: number, language?: string): string {
    if (isChinese(language)) return `${formatTimeFromMs(startMs, 'zh')}–${formatTimeFromMs(endMs, 'zh')}`;
    const clock = new Intl.DateTimeFormat(language || undefined, { hour: 'numeric', minute: '2-digit', hour12: true });
    // Across midnight formatRange adds dates; the times alone say it: "11:00 PM–12:00 AM".
    if (new Date(startMs).toDateString() !== new Date(endMs).toDateString()) {
        return `${clock.format(startMs)}–${clock.format(endMs)}`.replace(/[\u202f\u00a0]/g, ' ');
    }
    return clock.formatRange(startMs, endMs).replace(/\s*[–-]\s*/, '–').replace(/[\u202f\u00a0]/g, ' ');
}

export function dayStartMs(d: Date): number {
    const start = new Date(d);
    start.setHours(0, 0, 0, 0);
    return start.getTime();
}

export function dayEndMs(d: Date): number {
    const end = new Date(d);
    end.setHours(23, 59, 59, 999);
    return end.getTime() + 1;
}

// ── CronJobs prop type ──

export interface CronJobsProp {
    jobs: CronJobState[];
    loading: boolean;
    toggleEnabled: (id: string, enabled: boolean) => void;
    deleteJob: (id: string) => Promise<boolean> | void;
    runNow: (id: string) => Promise<boolean> | void;
    updateJob: (id: string, patch: Record<string, unknown>) => Promise<void>;
    loadRuns: (jobId: string) => Promise<CronRunState[]>;
    loadRunLogs: (jobId: string, runId: string) => Promise<StreamItem[]>;
}

// ── Hook ──

// The selected day is owned by the caller (the Activity page header navigates
// it); the hook keeps only the data and the calendar-popover month state.
export function useCalendarData(
    cronJobs: CronJobsProp,
    selectedDate: Date,
    setSelectedDate: (date: Date) => void,
    heatmapEnabled = false,
    learnedEnabled = false,
) {
    const [displayMonth, setDisplayMonth] = useState(selectedDate);

    const selectedMonth = `${selectedDate.getFullYear()}-${selectedDate.getMonth()}`;
    useEffect(() => {
        setDisplayMonth(new Date(selectedDate.getFullYear(), selectedDate.getMonth(), 1));
    }, [selectedMonth]);
    const dateStr = formatDateStr(selectedDate);
    const monthKey = `${displayMonth.getFullYear()}-${String(displayMonth.getMonth() + 1).padStart(2, '0')}`;
    const [hoveredHour, setHoveredHour] = useState<number | null>(null);
    const loadSummary = useCallback(() => EnsureDailySummary(dateStr), [dateStr]);
    const summaryState = useActivitySessionQueryState(`summary:${dateStr}`, loadSummary);
    const dailyData = summaryState.data ?? null;
    const dayLoading = summaryState.loading;
    const daySummary = useDaySummaryRetry({
        key: `summary:${dateStr}`, load: loadSummary, dateStr,
        hasData: !!dailyData, failing: !dailyData && !dayLoading && !!summaryState.error,
    });
    useEffect(() => { if (dailyData) markPageContent('activity'); }, [dailyData]);
    const start = dayStartMs(selectedDate);
    const end = dayEndMs(selectedDate);
    const loadRuns = useCallback(() => GetCronRunsByDateRange(start, end), [start, end]);
    const runs = useActivitySessionQuery(`runs:${dateStr}`, loadRuns);
    const [deletedJobs, setDeletedJobs] = useState<Set<string>>(new Set());
    const automationRuns = useMemo(() => (runs ?? []).filter((run) => !deletedJobs.has(run.jobId)), [runs, deletedJobs]);
    // The library reads are independent of the first-row and summary gates.
    // Share their result with the date picker instead of scanning twice.
    const allKnowledge = useActivitySessionQuery('knowledge', GetKnowledgeEntries, learnedEnabled || heatmapEnabled || !!dailyData);
    const allMemory = useActivitySessionQuery('memory', GetMemoryEntries, learnedEnabled || heatmapEnabled || !!dailyData);
    const knowledge = (allKnowledge ?? []).filter((entry) => entry.date?.startsWith(dateStr));
    const memoryEntries = (allMemory ?? []).filter((entry) => entry.modTime && formatDateStr(new Date(entry.modTime)) === dateStr);
    const createdAutomations = cronJobs.jobs.filter((job) => job.createdAt && formatDateStr(new Date(job.createdAt)) === dateStr && !deletedJobs.has(job.id));
    const loadMonth = useCallback(async () => {
        const month = new Date(monthKey + '-01T00:00:00');
        const first = new Date(month);
        first.setDate(first.getDate() - (first.getDay() + 6) % 7);
        const end = new Date(first);
        end.setDate(end.getDate() + 42);
        const [monthStats, monthRuns, allJobs] = await Promise.all([
            GetActivityMonthStats(monthKey),
            GetCronRunsByDateRange(first.getTime(), end.getTime()).catch(() => []),
            ListCronJobs().catch(() => []),
        ]);
        const map = new Map<string, DayHeatmapStats>();

        const emptyStats = (): DayHeatmapStats => ({
            activeMs: 0, sessionCount: 0, automationCreatedCount: 0, automationRunCount: 0, knowledgeCount: 0, memoryCount: 0,
        });

        for (const day of monthStats ?? []) {
            map.set(day.date, { ...emptyStats(), activeMs: day.activeMs, sessionCount: day.sessionCount });
        }

        for (const job of allJobs ?? []) {
            if (!job.createdAt) continue;
            const dateStr = formatDateStr(new Date(job.createdAt));
            const entry = map.get(dateStr);
            if (entry) {
                entry.automationCreatedCount++;
            } else {
                map.set(dateStr, { ...emptyStats(), automationCreatedCount: 1 });
            }
        }

        for (const r of monthRuns || []) {
            if (!r.startedAt) continue;
            const dateStr = formatDateStr(new Date(r.startedAt));
            const entry = map.get(dateStr);
            if (entry) {
                entry.automationRunCount++;
            } else {
                map.set(dateStr, { ...emptyStats(), automationRunCount: 1 });
            }
        }

        return map;
    }, [monthKey]);
    const monthStats = useActivitySessionQuery(`month:${monthKey}`, loadMonth, heatmapEnabled, false);
    const heatmapData = useMemo(() => {
        const map = new Map<string, DayHeatmapStats>();
        for (const [date, stats] of monthStats ?? []) map.set(date, { ...stats });
        // The same entries and date rules as the old heatmap, loaded once.
        const add = (date: string, kind: 'knowledgeCount' | 'memoryCount') => {
            const stats = map.get(date) ?? { activeMs: 0, sessionCount: 0, automationCreatedCount: 0, automationRunCount: 0, knowledgeCount: 0, memoryCount: 0 };
            stats[kind]++;
            map.set(date, stats);
        };
        for (const entry of allKnowledge ?? []) if (entry.date) add(entry.date.substring(0, 10), 'knowledgeCount');
        for (const entry of allMemory ?? []) if (entry.modTime) add(formatDateStr(new Date(entry.modTime)), 'memoryCount');
        return map;
    }, [monthStats, allKnowledge, allMemory]);
    const heatmapLoading = heatmapEnabled && !monthStats;

    // ── Derived values ──

    const hourRange = useMemo(() => {
        if (!dailyData?.hourlyActive) return { start: 6, end: 23 };
        const active = dailyData.hourlyActive;
        let start = 6;
        let end = 23;
        for (let i = 0; i < 24; i++) {
            if (active[i] > 0) { start = Math.min(start, i); break; }
        }
        for (let i = 23; i >= 0; i--) {
            if (active[i] > 0) { end = Math.max(end, i); break; }
        }
        return { start: Math.max(0, start - 1), end: Math.min(23, end + 1) };
    }, [dailyData]);

    const maxHourly = useMemo(() => {
        if (!dailyData?.hourlyActive) return 1;
        return Math.max(...dailyData.hourlyActive, 1);
    }, [dailyData]);

    // ── Action handlers ──

    const handleDeleteAutomation = useCallback((jobId: string) => {
        void Promise.resolve(cronJobs.deleteJob(jobId)).then((deleted) => {
            // A refused delete resolves false and keeps the schedule listed.
            if (deleted === false) return;
            setDeletedJobs((prev) => new Set(prev).add(jobId));
        });
    }, [cronJobs]);

    const reloadDailySummary = useCallback(async () => {
        invalidateActivitySessionCache();
    }, []);

    return {
        // Calendar state (the selected day itself is lifted to the caller)
        selectedDate,
        setSelectedDate,
        displayMonth,
        setDisplayMonth,
        heatmapData,
        heatmapLoading,

        // Day data
        dailyData,
        dayLoading,
        dayPending: dayLoading || daySummary.pending,
        dayFailed: daySummary.failed,
        retryDay: daySummary.retryNow,
        learnedLoading: allKnowledge === undefined || allMemory === undefined,
        automationRuns,
        createdAutomations,
        knowledge,
        memoryEntries,

        // UI state
        hoveredHour,
        setHoveredHour,

        // Derived
        hourRange,
        maxHourly,

        // Actions
        handleDeleteAutomation,
        reloadDailySummary,
    };
}
