import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { appActive, resetAppActivity, useActiveClock, useBudgetedRefresh } from './app-activity';

const runtime = vi.hoisted(() => ({ listeners: new Map<string, (data?: unknown) => void>() }));
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, listener: (data?: unknown) => void) => {
        runtime.listeners.set(name, listener);
        return () => runtime.listeners.delete(name);
    },
}));

let visibility: ReturnType<typeof vi.spyOn>;
function setHidden(hidden: boolean) {
    visibility.mockReturnValue(hidden ? 'hidden' : 'visible');
    document.dispatchEvent(new Event('visibilitychange'));
}

beforeEach(() => {
    resetAppActivity();
    runtime.listeners.clear();
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
});
afterEach(() => {
    resetAppActivity();
    visibility.mockRestore();
    vi.useRealTimers();
});

// Snappy 1.0, section 1, rule 2: when the window is hidden or the Mac is
// locked, the UI does nothing; on show it catches up only what is past its
// budget. "Done when": with the window hidden no UI timer fires.
describe('the window being looked at', () => {
    it('runs a budgeted resync only while shown, and on show only past its budget', async () => {
        const refresh = vi.fn();
        renderHook(() => useBudgetedRefresh(refresh, 15 * 60_000));
        await act(async () => { await vi.advanceTimersByTimeAsync(14 * 60_000); });
        expect(refresh).not.toHaveBeenCalled();
        await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
        expect(refresh).toHaveBeenCalledTimes(1);
        // Focus within the budget paints from the cache: no read.
        act(() => { window.dispatchEvent(new Event('focus')); });
        expect(refresh).toHaveBeenCalledTimes(1);
        // Hidden: no timer fires at all, however long.
        act(() => setHidden(true));
        expect(vi.getTimerCount()).toBe(0);
        await act(async () => { await vi.advanceTimersByTimeAsync(3 * 60 * 60_000); });
        expect(refresh).toHaveBeenCalledTimes(1);
        // Shown again past the budget: one catch-up, then the timer resumes.
        act(() => setHidden(false));
        expect(refresh).toHaveBeenCalledTimes(2);
        expect(vi.getTimerCount()).toBe(1);
    });

    it('treats a locked or sleeping Mac as not looked at, and catches up on unlock', async () => {
        const refresh = vi.fn();
        renderHook(() => useBudgetedRefresh(refresh, 10 * 60_000));
        act(() => { runtime.listeners.get('system:state')?.('locked'); });
        expect(appActive()).toBe(false);
        expect(vi.getTimerCount()).toBe(0);
        await act(async () => { await vi.advanceTimersByTimeAsync(60 * 60_000); });
        expect(refresh).not.toHaveBeenCalled();
        act(() => { runtime.listeners.get('system:state')?.('unlocked'); });
        expect(appActive()).toBe(true);
        expect(refresh).toHaveBeenCalledTimes(1);
        act(() => { runtime.listeners.get('system:state')?.('sleep'); });
        expect(appActive()).toBe(false);
        act(() => { runtime.listeners.get('system:state')?.('wake'); });
        expect(appActive()).toBe(true);
    });

    it('a click or key press means the Mac is unlocked even if "unlocked" was missed', async () => {
        const refresh = vi.fn();
        renderHook(() => useBudgetedRefresh(refresh, 10 * 60_000));
        act(() => { runtime.listeners.get('system:state')?.('locked'); });
        expect(appActive()).toBe(false);
        await act(async () => { await vi.advanceTimersByTimeAsync(60 * 60_000); });
        // No "unlocked" event arrives; the person clicks in the window.
        act(() => { window.dispatchEvent(new Event('pointerdown')); });
        expect(appActive()).toBe(true);
        expect(refresh).toHaveBeenCalledTimes(1);
        act(() => { runtime.listeners.get('system:state')?.('locked'); });
        act(() => { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' })); });
        expect(appActive()).toBe(true);
        act(() => { runtime.listeners.get('system:state')?.('locked'); });
        act(() => { window.dispatchEvent(new Event('focus')); });
        expect(appActive()).toBe(true);
    });

    it('stops a local clock while hidden and brings it current on show', async () => {
        vi.setSystemTime(new Date(2026, 9, 27, 9, 0));
        const { result } = renderHook(() => useActiveClock(60_000));
        const start = result.current;
        await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
        expect(result.current).toBe(start + 60_000);
        act(() => setHidden(true));
        expect(vi.getTimerCount()).toBe(0);
        await act(async () => { await vi.advanceTimersByTimeAsync(30 * 60_000); });
        expect(result.current).toBe(start + 60_000);
        act(() => setHidden(false));
        expect(result.current).toBe(start + 31 * 60_000);
    });

    it('starts no timer for a disabled resync', () => {
        renderHook(() => useBudgetedRefresh(vi.fn(), 60_000, false));
        expect(vi.getTimerCount()).toBe(0);
    });
});
