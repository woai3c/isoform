import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ACTIVITY_LIVE_BUDGET_MS, invalidateActivitySessionCache, resetActivitySessionCache, useActivitySessionQuery, useActivitySessionQueryState } from './activity-session-cache';
import { resetPagePrivacyWithdrawal } from '@/lib/page-privacy-withdrawal';

const events = vi.hoisted(() => new Map<string, (data?: any) => void>());
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: (name: string, fn: (data?: any) => void) => {
    events.set(name, fn);
    return () => { if (events.get(name) === fn) events.delete(name); };
} }));

beforeEach(() => { resetPagePrivacyWithdrawal(); resetActivitySessionCache(); events.clear(); });
afterEach(() => { resetPagePrivacyWithdrawal(); resetActivitySessionCache(); vi.useRealTimers(); });

// Snappy 1.0: capture events say today moved on every few minutes; today's
// Activity applies them at most every 5 minutes while the window is looked
// at, and catches up when it is shown again.
describe("Activity's live budget", () => {
    it('applies capture events at most every 5 minutes, and waits while hidden', async () => {
        vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] });
        const load = vi.fn().mockResolvedValue('rows');
        const { unmount } = renderHook(() => useActivitySessionQuery('activities:day', load));
        await act(async () => { await vi.advanceTimersByTimeAsync(0); });
        expect(load).toHaveBeenCalledTimes(1);
        // The first event after a quiet stretch applies at once.
        await act(async () => { events.get('activity:session-saved')?.(); await vi.advanceTimersByTimeAsync(0); });
        expect(load).toHaveBeenCalledTimes(2);
        // Three more within the budget make one read, when it ends.
        for (const name of ['activity:session-saved', 'activity:status', 'activity:moments-ready']) {
            await act(async () => { events.get(name)?.(); await vi.advanceTimersByTimeAsync(30_000); });
        }
        expect(load).toHaveBeenCalledTimes(2);
        await act(async () => { await vi.advanceTimersByTimeAsync(ACTIVITY_LIVE_BUDGET_MS); });
        expect(load).toHaveBeenCalledTimes(3);
        // Hidden: nothing is read until the window is shown again.
        const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
        await act(async () => { events.get('activity:session-saved')?.(); await vi.advanceTimersByTimeAsync(ACTIVITY_LIVE_BUDGET_MS * 2); });
        expect(load).toHaveBeenCalledTimes(3);
        visibility.mockReturnValue('visible');
        await act(async () => { document.dispatchEvent(new Event('visibilitychange')); await vi.advanceTimersByTimeAsync(0); });
        expect(load).toHaveBeenCalledTimes(4);
        visibility.mockRestore();
        unmount();
    });

    it('still applies an edit, Forget or protection at once', async () => {
        vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] });
        const load = vi.fn().mockResolvedValue('rows');
        renderHook(() => useActivitySessionQuery('activities:day', load));
        await act(async () => { events.get('activity:session-saved')?.(); await vi.advanceTimersByTimeAsync(0); });
        const reads = load.mock.calls.length;
        await act(async () => { events.get('activity:activities-updated')?.(); await vi.advanceTimersByTimeAsync(0); });
        expect(load.mock.calls.length).toBe(reads + 1);
    });
});

describe('Activity session cache ownership', () => {
    it('invalidates while unmounted and cannot refill from a superseded request', async () => {
        let finishOld!: (data: string) => void;
        const load = vi.fn().mockImplementationOnce(() => new Promise<string>((resolve) => { finishOld = resolve; })).mockResolvedValue('new activity');
        const first = renderHook(() => useActivitySessionQuery('activities:day', load));
        await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
        first.unmount();
        act(() => events.get('activity:session-saved')?.());
        const next = renderHook(() => useActivitySessionQuery('activities:day', load));
        await waitFor(() => expect(next.result.current).toBe('new activity'));
        await act(async () => finishOld('old activity'));
        expect(next.result.current).toBe('new activity');
        next.unmount();
        const reopen = renderHook(() => useActivitySessionQuery('activities:day', load));
        expect(reopen.result.current).toBe('new activity');
    });

    it.each(['closed-eyes:changed', 'memory:updated', 'knowledge:updated', 'background-listening:changed', 'permissions:changed', 'sensitive-apps:updated'])('keeps rows during refresh and clears them immediately on %s', async (event) => {
        const load = vi.fn().mockResolvedValueOnce('kept').mockImplementation(() => new Promise(() => {}));
        const { result } = renderHook(() => useActivitySessionQuery('activities:day', load));
        await waitFor(() => expect(result.current).toBe('kept'));
        act(() => invalidateActivitySessionCache());
        expect(result.current).toBe('kept');
        act(() => events.get(event)?.());
        expect(result.current).toBeUndefined();
    });

    it.each(['focus', 'visibilitychange'])('revalidates only stale Activity reads on %s while mounted', async (event) => {
        const load = vi.fn().mockResolvedValue('before');
        const first = renderHook(() => useActivitySessionQuery('summary:2026-10-22', load));
        await waitFor(() => expect(first.result.current).toBe('before'));
        const resume = () => {
            if (event === 'focus') window.dispatchEvent(new Event(event));
            else document.dispatchEvent(new Event(event));
        };
        await act(async () => resume());
        expect(first.result.current).toBe('before');
        expect(load).toHaveBeenCalledTimes(1);
        first.unmount();
        act(() => resume());
        const reopen = renderHook(() => useActivitySessionQuery('summary:2026-10-22', load));
        expect(reopen.result.current).toBe('before');
        expect(load).toHaveBeenCalledTimes(1);
        load.mockResolvedValue('after focus');
        vi.useFakeTimers();
        // Snappy 1.0: today's Activity may be 5 minutes old before a resume
        // reads it again.
        vi.setSystemTime(Date.now() + 61_000);
        await act(async () => resume());
        expect(load).toHaveBeenCalledTimes(1);
        vi.setSystemTime(Date.now() + ACTIVITY_LIVE_BUDGET_MS);
        await act(async () => resume());
        expect(reopen.result.current).toBe('after focus');
        expect(load).toHaveBeenCalledTimes(2);
        reopen.unmount();
    });

    it('expires the session at a local day change', async () => {
        const load = vi.fn().mockResolvedValueOnce('yesterday').mockImplementation(() => new Promise(() => {}));
        const { result } = renderHook(() => useActivitySessionQuery('activities:day', load));
        await waitFor(() => expect(result.current).toBe('yesterday'));
        vi.useFakeTimers();
        vi.setSystemTime(new Date(Date.now() + 86400_000));
        act(() => window.dispatchEvent(new Event('focus')));
        expect(result.current).toBeUndefined();
    });

    it('retries a failed month when the picker opens again', async () => {
        const load = vi.fn().mockRejectedValueOnce(new Error('temporarily unavailable')).mockResolvedValue('dots');
        const { result, rerender } = renderHook(({ open }) => useActivitySessionQuery('month:2026-10', load, open, false), { initialProps: { open: true } });
        await act(async () => {});
        expect(result.current).toBeUndefined();
        rerender({ open: false });
        rerender({ open: true });
        await waitFor(() => expect(result.current).toBe('dots'));
    });
});

it('settles failed reads and retries when the page reopens', async () => {
    resetActivitySessionCache();
    const load = vi.fn().mockRejectedValueOnce(new Error('temporary read failure')).mockResolvedValue('recovered');
    const first = renderHook(() => useActivitySessionQueryState('threads:failed', load));
    await waitFor(() => expect(first.result.current.loading).toBe(false));
    expect(first.result.current.error).toBeInstanceOf(Error);
    first.unmount();
    const next = renderHook(() => useActivitySessionQueryState('threads:failed', load));
    await waitFor(() => expect(next.result.current.data).toBe('recovered'));
    expect(next.result.current.error).toBeUndefined();
    next.unmount();
    resetActivitySessionCache();
});

it('keeps mounted query data when history grows beyond the cache limit', async () => {
    resetActivitySessionCache();
    const load = vi.fn().mockResolvedValue('visible rows');
    const pinned = renderHook(() => useActivitySessionQuery('activities:visible', load));
    await waitFor(() => expect(pinned.result.current).toBe('visible rows'));
    for (let i = 0; i < 80; i++) {
        const page = renderHook(() => useActivitySessionQuery(`history:${i}`, load));
        await act(async () => {});
        page.unmount();
    }
    pinned.rerender();
    expect(pinned.result.current).toBe('visible rows');
    expect(load).toHaveBeenCalledTimes(81);
    pinned.unmount();
    resetActivitySessionCache();
});

it.each(['activities', 'threads'])('opens a prefetched past %s day without another read after TTL, focus, or live capture', async (kind) => {
    resetActivitySessionCache();
    const { prefetchActivitySessionData } = await import('./activity-session-cache');
    const date = new Date();
    date.setDate(date.getDate() - 2);
    const key = `${kind}:${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
    const load = vi.fn().mockResolvedValue('computed day');
    await act(async () => { await prefetchActivitySessionData(key, load); });
    const first = renderHook(() => useActivitySessionQuery(key, load));
    expect(first.result.current).toBe('computed day');
    first.unmount();
    const oldNow = Date.now();
    vi.spyOn(Date, 'now').mockReturnValue(oldNow + 120_000);
    act(() => window.dispatchEvent(new Event('focus')));
    act(() => events.get('activity:session-saved')?.());
    act(() => events.get('activity:activities-updated')?.({ live: true }));
    const next = renderHook(() => useActivitySessionQuery(key, load));
    expect(next.result.current).toBe('computed day');
    await act(async () => {});
    expect(load).toHaveBeenCalledTimes(1);
    next.unmount();
    vi.restoreAllMocks();
    resetActivitySessionCache();
});

it.each(['memory:updated', 'sensitive-apps:updated', 'closed-eyes:changed', 'activity:activities-updated'])('discards a hidden completed day immediately on %s', async (event) => {
    resetActivitySessionCache();
    const date = new Date();
    date.setDate(date.getDate() - 2);
    const key = `threads:${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
    const load = vi.fn().mockResolvedValueOnce('before privacy change').mockImplementation(() => new Promise(() => {}));
    const first = renderHook(() => useActivitySessionQuery(key, load));
    await waitFor(() => expect(first.result.current).toBe('before privacy change'));
    first.unmount();
    act(() => events.get(event)?.());
    const next = renderHook(() => useActivitySessionQuery(key, load));
    expect(next.result.current).toBeUndefined();
    await waitFor(() => expect(load).toHaveBeenCalledTimes(2));
    next.unmount();
    resetActivitySessionCache();
});

it('does not supersede an in-flight past-day prefetch when the live day changes', async () => {
    resetActivitySessionCache();
    const { prefetchActivitySessionData } = await import('./activity-session-cache');
    const key = (date: Date) => `threads:${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
    const past = new Date();
    past.setDate(past.getDate() - 2);
    let finish!: (value: string) => void;
    const oldLoad = vi.fn().mockImplementation(() => new Promise<string>((resolve) => { finish = resolve; }));
    const pending = prefetchActivitySessionData(key(past), oldLoad);
    const live = renderHook(() => useActivitySessionQuery(key(new Date()), async () => 'live day'));
    await waitFor(() => expect(live.result.current).toBe('live day'));
    act(() => events.get('activity:status')?.());
    await act(async () => { finish('computed history'); await pending; });
    expect(oldLoad).toHaveBeenCalledTimes(1);
    const reopened = renderHook(() => useActivitySessionQuery(key(past), oldLoad));
    expect(reopened.result.current).toBe('computed history');
    live.unmount();
    reopened.unmount();
    resetActivitySessionCache();
});

it('retries an unfinished historical grouping after the normal TTL', async () => {
    resetActivitySessionCache();
    const past = new Date();
    past.setDate(past.getDate() - 2);
    const key = `threads:${past.getFullYear()}-${String(past.getMonth() + 1).padStart(2, '0')}-${String(past.getDate()).padStart(2, '0')}`;
    const load = vi.fn().mockResolvedValueOnce({ grouped: false, threads: [] }).mockResolvedValue({ grouped: true, threads: [] });
    const first = renderHook(() => useActivitySessionQuery<{ grouped: boolean }>(key, load));
    await waitFor(() => expect(first.result.current?.grouped).toBe(false));
    first.unmount();
    vi.spyOn(Date, 'now').mockReturnValue(Date.now() + ACTIVITY_LIVE_BUDGET_MS + 1);
    const next = renderHook(() => useActivitySessionQuery<{ grouped: boolean }>(key, load));
    await waitFor(() => expect(next.result.current?.grouped).toBe(true));
    expect(load).toHaveBeenCalledTimes(2);
    next.unmount();
    vi.restoreAllMocks();
    resetActivitySessionCache();
});

it('retains an already completed day when the local calendar advances', async () => {
    resetActivitySessionCache();
    const past = new Date();
    past.setDate(past.getDate() - 2);
    const key = `threads:${past.getFullYear()}-${String(past.getMonth() + 1).padStart(2, '0')}-${String(past.getDate()).padStart(2, '0')}`;
    const load = vi.fn().mockResolvedValue({ grouped: true, threads: [] });
    const first = renderHook(() => useActivitySessionQuery<{ grouped: boolean }>(key, load));
    await waitFor(() => expect(first.result.current?.grouped).toBe(true));
    vi.useFakeTimers();
    vi.setSystemTime(new Date(Date.now() + 86400_000));
    await act(async () => { window.dispatchEvent(new Event('focus')); });
    expect(first.result.current?.grouped).toBe(true);
    expect(load).toHaveBeenCalledTimes(1);
    first.unmount();
    vi.useRealTimers();
    resetActivitySessionCache();
});

function historicalKey(kind = 'activities', offset = -2) {
    const date = new Date(); date.setDate(date.getDate() + offset);
    return `${kind}:${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
}

it('withdraws a prefetched backfill day and changed carry while retaining unrelated history', async () => {
    resetActivitySessionCache();
    const { prefetchActivitySessionData } = await import('./activity-session-cache');
    const keys = [historicalKey('activities', -3), historicalKey('threads', -3), historicalKey('activities'), historicalKey('threads'), historicalKey('activities', -7)];
    const loads = keys.map(() => vi.fn().mockResolvedValueOnce('initial').mockResolvedValue('late composed row'));
    await act(async () => { for (let i = 0; i < keys.length; i++) await prefetchActivitySessionData(keys[i], loads[i]); });
    act(() => events.get('activity:activities-updated')?.({ dates: [keys[0].split(':')[1], keys[2].split(':')[1]] }));
    vi.spyOn(Date, 'now').mockReturnValue(Date.now() + 120_000);
    act(() => window.dispatchEvent(new Event('focus')));
    for (let i = 0; i < keys.length; i++) {
        const page = renderHook(() => useActivitySessionQuery(keys[i], loads[i]));
        await waitFor(() => expect(page.result.current).toBe(i < 4 ? 'late composed row' : 'initial'));
        expect(loads[i]).toHaveBeenCalledTimes(i < 4 ? 2 : 1);
        page.unmount();
    }
    vi.restoreAllMocks(); resetActivitySessionCache();
});

const boundaries: Array<[string, unknown]> = [
    ['memory:updated', undefined], ['sensitive-apps:updated', undefined],
    ['closed-eyes:changed', true], ['background-listening:changed', false],
    ['permissions:changed', undefined], ['knowledge:updated', undefined],
    ['activity:activities-updated', undefined], ['model-accounts:updated', { accounts: [] }],
];

it.each(boundaries)('clears hidden completed data immediately and recovers across %s', async (event, payload) => {
    resetActivitySessionCache();
    const { pageQueryCache } = await import('@/lib/query-cache');
    const key = historicalKey();
    const load = vi.fn().mockResolvedValueOnce('private source').mockResolvedValueOnce('withdrawn').mockResolvedValue('legitimate recovery');
    const first = renderHook(() => useActivitySessionQuery(key, load));
    await waitFor(() => expect(first.result.current).toBe('private source')); first.unmount();
    act(() => events.get(event)?.(payload));
    const start = new Date(key.split(':')[1] + 'T00:00:00');
    const end = new Date(start.getFullYear(), start.getMonth(), start.getDate() + 1);
    expect(pageQueryCache.peek(`Activity:past:GetActivitiesInRange:${key.split(':')[1]}`,  [start.getTime(), end.getTime()])).toBeUndefined();
    vi.spyOn(Date, 'now').mockReturnValue(Date.now() + 120_000);
    const withdrawn = renderHook(() => useActivitySessionQuery(key, load));
    await waitFor(() => expect(withdrawn.result.current).toBe('withdrawn')); withdrawn.unmount();
    act(() => events.get(event)?.(payload));
    const recovered = renderHook(() => useActivitySessionQuery(key, load));
    const repeatedEmptyAccounts = event === 'model-accounts:updated';
    await waitFor(() => expect(recovered.result.current).toBe(repeatedEmptyAccounts ? 'withdrawn' : 'legitimate recovery'));
    expect(load).toHaveBeenCalledTimes(repeatedEmptyAccounts ? 2 : 3); recovered.unmount();
    vi.restoreAllMocks(); resetActivitySessionCache();
});

it.each([...boundaries, ['activity:activities-updated', { dates: [historicalKey().split(':')[1]] }] as [string, unknown]])('fences a late hidden prefetch across %s (%j)', async (event, payload) => {
    resetActivitySessionCache();
    const { prefetchActivitySessionData } = await import('./activity-session-cache');
    const key = historicalKey();
    let finish!: (value: string) => void;
    const old = vi.fn().mockImplementationOnce(() => new Promise<string>((resolve) => { finish = resolve; })).mockResolvedValue('withdrawn');
    const pending = prefetchActivitySessionData(key, old);
    await waitFor(() => expect(old).toHaveBeenCalledTimes(1));
    act(() => events.get(event)?.(payload));
    const fresh = vi.fn().mockResolvedValue('withdrawn');
    const next = renderHook(() => useActivitySessionQuery(key, fresh));
    await waitFor(() => expect(next.result.current).toBe('withdrawn'));
    await act(async () => { finish('stale private completion'); await pending; });
    expect(next.result.current).toBe('withdrawn'); next.unmount();
    const reopened = renderHook(() => useActivitySessionQuery(key, fresh));
    expect(reopened.result.current).toBe('withdrawn');
    expect(fresh).toHaveBeenCalledTimes(1); reopened.unmount(); resetActivitySessionCache();
});

it('distinguishes account identity withdrawal from ordinary quotas without erasing unrelated work', async () => {
    resetActivitySessionCache();
    const { pageQueryCache } = await import('@/lib/query-cache');
    pageQueryCache.set('local-user-draft', ['main'], 'editable user work');
    const load = vi.fn().mockResolvedValueOnce('history').mockResolvedValueOnce('after first observation').mockResolvedValue('identity rechecked');
    const key = historicalKey();
    const first = renderHook(() => useActivitySessionQuery(key, load));
    await waitFor(() => expect(first.result.current).toBe('history')); first.unmount();
    const account = { provider: 'test-provider', kind: 'subscription', name: 'first@example.test', on: true };
    act(() => events.get('model-accounts:updated')?.({ accounts: [account] }));
    const quota = renderHook(() => useActivitySessionQuery(key, load));
    await waitFor(() => expect(quota.result.current).toBe('after first observation'));
    act(() => events.get('model-accounts:updated')?.({ accounts: [{ ...account, balance: '0', error: 'quota', windows: [] }] }));
    expect(quota.result.current).toBe('after first observation'); expect(load).toHaveBeenCalledTimes(2); quota.unmount();
    act(() => events.get('model-accounts:updated')?.({ accounts: [{ ...account, name: 'second@example.test' }] }));
    const changed = renderHook(() => useActivitySessionQuery(key, load));
    await waitFor(() => expect(changed.result.current).toBe('identity rechecked'));
    expect(pageQueryCache.peek('local-user-draft', ['main'])).toBe('editable user work');
    changed.unmount(); resetActivitySessionCache();
});

it('does not restart an unaffected historical prefetch when another civil day changes', async () => {
    resetActivitySessionCache();
    const { prefetchActivitySessionData } = await import('./activity-session-cache');
    let finish!: (value: string) => void;
    const load = vi.fn().mockImplementationOnce(() => new Promise<string>((resolve) => { finish = resolve; })).mockResolvedValue('unexpected reread');
    const key = historicalKey('threads', -7);
    const pending = prefetchActivitySessionData(key, load);
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
    act(() => events.get('activity:activities-updated')?.({ dates: [historicalKey().split(':')[1]] }));
    await act(async () => { finish('unaffected history'); await pending; });
    const reopened = renderHook(() => useActivitySessionQuery(key, load));
    expect(reopened.result.current).toBe('unaffected history');
    expect(load).toHaveBeenCalledTimes(1); reopened.unmount(); resetActivitySessionCache();
});
