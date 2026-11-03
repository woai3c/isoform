import { resetActivitySessionCache } from './activity-session-cache';
import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useCalendarData, type CronJobsProp } from './useCalendarData';

const fixture = vi.hoisted(() => ({
    bindings: new Map<string, ReturnType<typeof vi.fn>>(),
    listeners: new Map<string, Set<(payload: unknown) => void>>(),
}));

vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        if (!fixture.bindings.has(name)) fixture.bindings.set(name, vi.fn().mockResolvedValue([]));
        return fixture.bindings.get(name);
    },
}));

vi.mock('../../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, handler: (payload: unknown) => void) => {
        const listeners = fixture.listeners.get(name) ?? new Set();
        listeners.add(handler);
        fixture.listeners.set(name, listeners);
        return () => listeners.delete(handler);
    },
}));

function binding(name: string) {
    if (!fixture.bindings.has(name)) fixture.bindings.set(name, vi.fn().mockResolvedValue([]));
    return fixture.bindings.get(name)!;
}

const PAST_DAY = new Date(2026, 9, 12);
const cronJobs: CronJobsProp = {
    jobs: [],
    loading: false,
    toggleEnabled: vi.fn(),
    deleteJob: vi.fn(),
    runNow: vi.fn(),
    updateJob: vi.fn().mockResolvedValue(undefined),
    loadRuns: vi.fn().mockResolvedValue([]),
    loadRunLogs: vi.fn().mockResolvedValue([]),
};

describe('useCalendarData — the day summary comes from Go', () => {
    beforeEach(() => {
        resetActivitySessionCache();
        fixture.bindings.forEach((mock) => mock.mockReset().mockResolvedValue([]));
        fixture.listeners.clear();
        // A past day with activity and only a partial digest: the old page
        // started GenerateDailySummary itself here.
        binding('EnsureDailySummary').mockResolvedValue({
            date: '2026-10-12', totalActiveMs: 3600_000, aiSummary: 'Partial', aiSummaryStatus: 'done',
            isPartial: true, autoStatus: 'refreshing', hourlyActive: [], appBreakdown: [],
        });
    });

    it('reads the day through EnsureDailySummary and never starts a summary itself', async () => {
        const { result } = renderHook(() => useCalendarData(cronJobs, PAST_DAY, vi.fn()));
        await waitFor(() => expect(result.current.dailyData?.autoStatus).toBe('refreshing'));
        expect(binding('EnsureDailySummary')).toHaveBeenCalledWith('2026-10-12');
        expect(binding('GenerateDailySummary')).not.toHaveBeenCalled();
        expect(binding('GetDailySummary')).not.toHaveBeenCalled();
    });

    it('reads the day again when its summary is ready or fails', async () => {
        renderHook(() => useCalendarData(cronJobs, PAST_DAY, vi.fn()));
        await waitFor(() => expect(binding('EnsureDailySummary')).toHaveBeenCalled());
        for (const event of ['activity:summary-ready', 'activity:summary-error']) {
            const before = binding('EnsureDailySummary').mock.calls.length;
            act(() => fixture.listeners.get(event)?.forEach((fn) => fn('2026-10-12')));
            await waitFor(() => expect(binding('EnsureDailySummary').mock.calls.length).toBeGreaterThan(before));
        }
    });
});

describe('Activity heatmap demand loading', () => {
    beforeEach(() => {
        resetActivitySessionCache();
        fixture.bindings.forEach((mock) => mock.mockReset().mockResolvedValue([]));
        fixture.listeners.clear();
    });

    it('loads one month batch only on open, keeps all dots and reuses it across day arrows and reopen', async () => {
        binding('GetActivityMonthStats').mockResolvedValue([{ date: '2026-10-12', activeMs: 60000, sessionCount: 2 }]);
        binding('GetKnowledgeEntries').mockResolvedValue([{ id: 'k', date: '2026-10-12' }]);
        binding('GetMemoryEntries').mockResolvedValue([{ id: 'm', modTime: PAST_DAY.getTime() }]);
        binding('ListCronJobs').mockResolvedValue([{ id: 'job', createdAt: PAST_DAY.getTime() }]);
        binding('GetCronRunsByDateRange').mockResolvedValue([{ id: 'run', startedAt: PAST_DAY.getTime() }]);
        const { result, rerender } = renderHook(({ date, open }) => useCalendarData(cronJobs, date, vi.fn(), open), { initialProps: { date: PAST_DAY, open: false } });
        await waitFor(() => expect(result.current.dayLoading).toBe(false));
        expect(binding('GetActivityMonthStats')).not.toHaveBeenCalled();
        expect(binding('GetWeeklySummary')).not.toHaveBeenCalled();
        rerender({ date: PAST_DAY, open: true });
        await waitFor(() => expect(result.current.heatmapData.get('2026-10-12')).toEqual({ activeMs: 60000, sessionCount: 2, automationCreatedCount: 1, automationRunCount: 1, knowledgeCount: 1, memoryCount: 1 }));
        rerender({ date: new Date(2026, 9, 13), open: false });
        rerender({ date: new Date(2026, 9, 13), open: true });
        expect(binding('GetActivityMonthStats')).toHaveBeenCalledTimes(1);
        expect(binding('GetWeeklySummary')).not.toHaveBeenCalled();
        act(() => fixture.listeners.get('activity:session-saved')?.forEach((fn) => fn(undefined)));
        await waitFor(() => expect(binding('GetActivityMonthStats')).toHaveBeenCalledTimes(2));
    });
});

it.each(['GetCronRunsByDateRange', 'ListCronJobs'])('keeps activity dots if optional %s fails', async (name) => {
    resetActivitySessionCache();
    fixture.bindings.forEach((mock) => mock.mockReset().mockResolvedValue([]));
    fixture.listeners.clear();
    binding('GetActivityMonthStats').mockResolvedValue([{ date: '2026-10-12', activeMs: 60000, sessionCount: 2 }]);
    binding(name).mockRejectedValue(new Error('temporary automation read failure'));
    const { result } = renderHook(() => useCalendarData(cronJobs, PAST_DAY, vi.fn(), true));
    await waitFor(() => expect(result.current.heatmapData.get('2026-10-12')?.activeMs).toBe(60000));
    expect(result.current.heatmapLoading).toBe(false);
});
