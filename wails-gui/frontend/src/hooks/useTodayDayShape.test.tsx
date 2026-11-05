import { act, cleanup, render, renderHook, screen, waitFor } from '@testing-library/react';
import { beforeEach, afterEach, expect, it, vi } from 'vitest';
import { resetPageQueries } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { TodayDayStrip } from '@/components/today/TodayDayStrip';
import { useTodayDayShape } from './useTodayDayShape';
import { dayShapeFixture } from '@/components/today/today-next.fixture';
import '@/i18n';
const mocks = vi.hoisted(() => ({ read: vi.fn(), off: vi.fn(), listeners: new Map<string, () => void>() }));
vi.mock('@/lib/wails-shim-app', () => ({ GetTodayDayShape: mocks.read }));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: (event: string, listener: () => void) => { mocks.listeners.set(event, listener); return mocks.off; } }));
beforeEach(() => { resetPageQueries(); vi.clearAllMocks(); mocks.listeners.clear(); mocks.read.mockReset().mockResolvedValue(dayShapeFixture); });
afterEach(() => { cleanup(); resetPageQueries(); vi.useRealTimers(); });
it('reads the local ISO date and refreshes from the backend event', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 9, 23, 9));
    const { result, unmount } = renderHook(() => useTodayDayShape(true));
    await waitFor(() => expect(result.current.shape).toEqual(dayShapeFixture));
    expect(mocks.read).toHaveBeenCalledWith('2026-10-23');
    mocks.read.mockResolvedValueOnce({ ...dayShapeFixture, now: '2026-10-23T10:01:00-07:00' });
    await act(async () => { await mocks.listeners.get('today-dayshape:updated')!(); });
    await waitFor(() => expect(result.current.shape?.now).toBe('2026-10-23T10:01:00-07:00'));
    unmount();
    mocks.read.mockClear();
    await act(async () => { await mocks.listeners.get('today-dayshape:updated')!(); });
    expect(mocks.read).not.toHaveBeenCalled();
});
it('discards stale reads and keeps the drawing on a failed refresh', async () => {
    let resolveOld: (value: unknown) => void = () => {};
    mocks.read.mockReturnValueOnce(new Promise((resolve) => { resolveOld = resolve; }));
    const { result } = renderHook(() => useTodayDayShape(true));
    await waitFor(() => expect(mocks.read).toHaveBeenCalledTimes(1));
    await act(async () => { await mocks.listeners.get('today-dayshape:updated')!(); });
    await waitFor(() => expect(result.current.shape).toEqual(dayShapeFixture));
    await act(async () => resolveOld({ ...dayShapeFixture, events: [] }));
    expect(result.current.shape?.events).toEqual(dayShapeFixture.events);
    const populated = result.current.shape;
    mocks.read.mockRejectedValueOnce(new Error('read failed'));
    await act(async () => { await mocks.listeners.get('today-dayshape:updated')!(); });
    await waitFor(() => expect(mocks.read).toHaveBeenCalledTimes(3));
    expect(result.current.shape).toBe(populated);
    expect(result.current.failed).toBe(true);
    expect(result.current.lastUpdatedAt).not.toBeNull();
    mocks.read.mockResolvedValueOnce(dayShapeFixture);
    act(() => result.current.retry());
    await waitFor(() => expect(result.current.failed).toBe(false));
});
it('distinguishes a failed first calendar read from a disconnected calendar', async () => {
    mocks.read.mockRejectedValue(new Error('offline'));
    const { result } = renderHook(() => useTodayDayShape(true));
    await waitFor(() => expect(result.current.failed).toBe(true));
    expect(result.current.shape).toBeNull();
    expect(result.current.lastUpdatedAt).toBeNull();
});
it('does not poll and reads once when the window is shown again', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'setTimeout', 'clearTimeout', 'Date'] });
    vi.setSystemTime(new Date(2026, 9, 23, 9));
    renderHook(() => useTodayDayShape(true));
    await act(async () => {});
    expect(mocks.read).toHaveBeenCalledTimes(1);
    await act(async () => { window.dispatchEvent(new Event('focus')); await vi.advanceTimersByTimeAsync(30 * 60_000); });
    expect(mocks.read).toHaveBeenCalledTimes(1);
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); await vi.advanceTimersByTimeAsync(30 * 60_000); });
    await act(async () => { await mocks.listeners.get('today-dayshape:updated')!(); });
    expect(mocks.read).toHaveBeenCalledTimes(1);
    visibility.mockReturnValue('visible');
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); await vi.advanceTimersByTimeAsync(0); });
    expect(mocks.read).toHaveBeenCalledTimes(2);
    visibility.mockRestore();
});
it('removes a deleted event from the rendered strip on a successful reread', async () => {
    function Strip() {
        const { shape, dateISO } = useTodayDayShape(true, '2026-10-23');
        return <TodayDayStrip shape={shape} requestedDate={dateISO} />;
    }
    render(<Strip />);
    await screen.findByTitle(/1:1 with Priya on Halcyon Meet/);
    const remaining = { ...dayShapeFixture, events: dayShapeFixture.events.filter((event) => event.id !== 'priya') };
    mocks.read.mockResolvedValueOnce(remaining);
    await act(async () => { await mocks.listeners.get('today-dayshape:updated')!(); });
    await waitFor(() => expect(screen.queryByTitle(/1:1 with Priya on Halcyon Meet/)).not.toBeInTheDocument());
    expect(screen.getByTitle(/Check-in with Maya Chen/)).toBeInTheDocument();
    expect(screen.getByTestId('today-day-strip')).toBeInTheDocument();
});
it('removes the strip when a successful reread removes every event', async () => {
    function Strip() {
        const { shape, dateISO } = useTodayDayShape(true, '2026-10-23');
        return <TodayDayStrip shape={shape} requestedDate={dateISO} />;
    }
    render(<Strip />);
    await screen.findByTitle(/1:1 with Priya on Halcyon Meet/);
    mocks.read.mockResolvedValueOnce({ ...dayShapeFixture, events: [], hold: null });
    await act(async () => { await mocks.listeners.get('today-dayshape:updated')!(); });
    // Today review v1.1.6 removes an empty day strip.
    await waitFor(() => expect(screen.queryByTestId('today-day-strip')).not.toBeInTheDocument());
    expect(screen.queryByTestId('today-day-event')).not.toBeInTheDocument();
    expect(screen.queryByTestId('today-day-hold')).not.toBeInTheDocument();
});
it('removes the strip when a successful reread disconnects the calendar', async () => {
    function Strip() {
        const { shape, dateISO } = useTodayDayShape(true, '2026-10-23');
        return <TodayDayStrip shape={shape} requestedDate={dateISO} />;
    }
    render(<Strip />);
    await screen.findByTestId('today-day-strip');
    mocks.read.mockResolvedValueOnce({ ...dayShapeFixture, events: [], hold: null, calendarConnected: false });
    await act(async () => { await mocks.listeners.get('today-dayshape:updated')!(); });
    await waitFor(() => expect(screen.queryByTestId('today-day-strip')).not.toBeInTheDocument());
    expect(screen.queryByTestId('today-day-strip-placeholder')).not.toBeInTheDocument();
});
it('renders the strip from the preloaded cache on its first paint', () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 9, 23, 9));
    pageQueryCache.set('GetTodayDayShape', ['2026-10-23'], dayShapeFixture);
    function Strip() {
        const { shape, dateISO } = useTodayDayShape(true, '2026-10-23');
        return <TodayDayStrip shape={shape} requestedDate={dateISO} />;
    }
    render(<Strip />);
    expect(screen.getByTestId('today-day-strip')).toBeInTheDocument();
    expect(screen.queryByTestId('today-day-strip-placeholder')).not.toBeInTheDocument();
});
it('shows the strip within one flush of the cold backend answering', async () => {
    let resolve!: (shape: unknown) => void;
    mocks.read.mockReturnValueOnce(new Promise((finish) => { resolve = finish; }));
    function Strip() {
        const { shape, dateISO } = useTodayDayShape(true, '2026-10-23');
        return <TodayDayStrip shape={shape} requestedDate={dateISO} />;
    }
    render(<Strip />);
    expect(screen.queryByTestId('today-day-strip')).not.toBeInTheDocument();
    await act(async () => { resolve(dayShapeFixture); });
    expect(screen.getByTestId('today-day-strip')).toBeInTheDocument();
    expect(screen.queryByTestId('today-day-strip-placeholder')).not.toBeInTheDocument();
});
it('does not start reads while Today is inactive', () => {
    renderHook(() => useTodayDayShape(false));
    expect(mocks.read).not.toHaveBeenCalled();
});
it('advances the marker and date after midnight, and stops reading when inactive', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 9, 23, 23, 59, 30));
    const { rerender } = renderHook(({ enabled }) => useTodayDayShape(enabled), { initialProps: { enabled: true } });
    await act(async () => {});
    expect(mocks.read).toHaveBeenLastCalledWith('2026-10-23');
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
    expect(mocks.read).toHaveBeenLastCalledWith('2026-10-24');
    rerender({ enabled: false });
    mocks.read.mockClear();
    await act(async () => { await vi.advanceTimersByTimeAsync(120_000); });
    expect(mocks.read).not.toHaveBeenCalled();
});
// An evening brief shows the rest of its own day; after midnight the strip is
// the new day's, across a month too (today-review v1.1.20 R44).
it('requests an evening brief\'s own day, then the new day after midnight, including month rollover', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 9, 31, 18, 30));
    const evening = renderHook(() => useTodayDayShape(true, '2026-10-31', true));
    await waitFor(() => expect(mocks.read).toHaveBeenCalledWith('2026-10-31'));
    evening.unmount();
    vi.setSystemTime(new Date(2026, 10, 1, 0, 30));
    renderHook(() => useTodayDayShape(true, '2026-10-31', true));
    await waitFor(() => expect(mocks.read).toHaveBeenCalledWith('2026-11-01'));
});
