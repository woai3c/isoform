import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { resetPageQueries } from '@/lib/page-queries';
import { QUIET_REFRESH_MS, useTodayQuietLines } from './useTodayQuietLines';

const mocks = vi.hoisted(() => ({ read: vi.fn(), listeners: new Map<string, (data?: unknown) => unknown>() }));
vi.mock('@/lib/wails-shim-app', () => ({ GetTodayQuietLines: mocks.read }));
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, listener: (data?: unknown) => unknown) => {
        mocks.listeners.set(name, listener);
        return () => mocks.listeners.delete(name);
    },
}));

beforeEach(() => {
    resetPageQueries();
    mocks.listeners.clear();
    mocks.read.mockReset().mockResolvedValue([{ id: 'one', kind: 'suggestion', text: 'A quiet line' }]);
});
afterEach(() => { resetPageQueries(); vi.useRealTimers(); vi.restoreAllMocks(); });

// Snappy 1.0: the quiet lines follow each brief and the notice settings, and
// otherwise read at most every 30 minutes while Today is visible (was 5).
it('reads on a brief and on showing, never on a timer or while hidden', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    const { result } = renderHook(() => useTodayQuietLines(true));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(result.current).toHaveLength(1);
    expect(mocks.read).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(5 * 60_000); });
    expect(mocks.read).toHaveBeenCalledTimes(1);
    await act(async () => { await mocks.listeners.get('brief:stored')?.('2026-10-27-morning'); });
    expect(mocks.read).toHaveBeenCalledTimes(2);
    await act(async () => { await vi.advanceTimersByTimeAsync(QUIET_REFRESH_MS); });
    expect(mocks.read).toHaveBeenCalledTimes(2);
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    act(() => { document.dispatchEvent(new Event('visibilitychange')); });
    await act(async () => { await vi.advanceTimersByTimeAsync(QUIET_REFRESH_MS * 3); });
    expect(mocks.read).toHaveBeenCalledTimes(2);
    visibility.mockReturnValue('visible');
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); await vi.advanceTimersByTimeAsync(0); });
    expect(mocks.read).toHaveBeenCalledTimes(3);
});

it('reads nothing while Today is not shown', async () => {
    renderHook(() => useTodayQuietLines(false));
    await act(async () => {});
    expect(mocks.read).not.toHaveBeenCalled();
});
