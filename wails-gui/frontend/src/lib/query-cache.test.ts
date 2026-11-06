import { describe, expect, it, vi } from 'vitest';
import { QueryCache } from './query-cache';

function deferred<T>() {
    let resolve!: (value: T) => void;
    let reject!: (error: unknown) => void;
    const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
    return { promise, resolve, reject };
}

describe('renderer query cache', () => {
    it('remembers a longer completed TTL on a fresh hit before page revalidation', async () => {
        let now = 0;
        const cache = new QueryCache(64, () => now);
        const load = vi.fn().mockResolvedValueOnce({ refreshing: true }).mockResolvedValue({ refreshing: false });
        await cache.read('GetTodayTopics', [], load, 2000);
        now = 2001;
        await cache.read('GetTodayTopics', [], load, 2000);
        await cache.state('GetTodayTopics')?.pending;
        await cache.read('GetTodayTopics', [], load, 60_000);
        now += 3000;
        cache.revalidate(['GetTodayTopics']);
        await Promise.resolve();
        expect(load).toHaveBeenCalledTimes(2);
        expect(cache.state('GetTodayTopics')?.ttl).toBe(60_000);
        now += 60_001;
        cache.revalidate(['GetTodayTopics']);
        await cache.state('GetTodayTopics')?.pending;
        expect(load).toHaveBeenCalledTimes(3);
    });

    it('on return marks only entries past their budget and tells only their readers', async () => {
        let now = 0;
        const cache = new QueryCache(64, () => now);
        const old = vi.fn().mockResolvedValue('old');
        const fresh = vi.fn().mockResolvedValue('fresh');
        await cache.read('GetAboutYou', [], old, 60_000);
        now = 50_000;
        await cache.read('GetMemoryStatus', [], fresh, 60_000);
        now = 70_000;
        const notified: string[] = [];
        const off = cache.subscribe((name) => notified.push(name));
        cache.invalidateStale(['GetAboutYou', 'GetMemoryStatus']);
        off();
        expect(notified).toEqual(['GetAboutYou']);
        expect(cache.state('GetAboutYou')?.invalid).toBe(true);
        expect(cache.state('GetMemoryStatus')?.invalid).toBe(false);
        await cache.read('GetMemoryStatus', [], fresh, 60_000);
        expect(fresh).toHaveBeenCalledTimes(1);
        await cache.read('GetAboutYou', [], old, 60_000);
        expect(old).toHaveBeenCalledTimes(2);
    });

    it('keys hits by binding and arguments and shares an in-flight miss', async () => {
        const cache = new QueryCache();
        const pending = deferred<string>();
        const load = vi.fn(() => pending.promise);
        const first = cache.read('Read', ['one'], load);
        const second = cache.read('Read', ['one'], load);
        await Promise.resolve();
        expect(load).toHaveBeenCalledTimes(1);
        pending.resolve('content');
        expect(await first).toBe('content');
        expect(await second).toBe('content');
        expect(await cache.read('Read', ['one'], load)).toBe('content');
        expect(cache.peek('Read', ['two'])).toBeUndefined();
        expect(cache.peek('Other', ['one'])).toBeUndefined();
        expect(load).toHaveBeenCalledTimes(1);
    });

    it('returns stale data immediately and publishes one background revalidation', async () => {
        let now = 0;
        const cache = new QueryCache(64, () => now);
        const updates = vi.fn();
        const off = cache.subscribe(updates);
        await cache.read('Read', [], async () => 'old');
        now = 60_001;
        const pending = deferred<string>();
        const load = vi.fn(() => pending.promise);
        expect(await cache.read('Read', [], load)).toBe('old');
        expect(await cache.read('Read', [], load)).toBe('old');
        expect(load).toHaveBeenCalledTimes(1);
        pending.resolve('new');
        await pending.promise;
        await Promise.resolve();
        expect(cache.peek('Read')).toBe('new');
        expect(updates).toHaveBeenCalledTimes(2);
        off();
        cache.invalidate();
        expect(updates).toHaveBeenCalledTimes(2);
    });

    it('withdraws invalidated data and prevents an old result from reaching any caller', async () => {
        const cache = new QueryCache();
        const old = deferred<string>();
        const load = vi.fn().mockImplementationOnce(() => old.promise).mockResolvedValue('permitted now');
        const first = cache.read('Read', [], load);
        await Promise.resolve();
        cache.invalidate(['Read']);
        expect(cache.peek('Read')).toBeUndefined();
        const current = cache.read('Read', [], load);
        expect(await current).toBe('permitted now');
        old.resolve('withdrawn text');
        expect(await first).toBe('permitted now');
        expect(load).toHaveBeenCalledTimes(2);
    });

    it('fences an evicted in-flight read when a later event withdraws its source', async () => {
        const cache = new QueryCache(1);
        const old = deferred<string>();
        const load = vi.fn().mockImplementationOnce(() => old.promise).mockResolvedValue('current');
        const first = cache.read('PrivateRead', [], load);
        await Promise.resolve();
        await cache.read('Other', [], async () => 'other');
        cache.invalidate(['PrivateRead']);
        old.resolve('withdrawn');
        expect(await first).toBe('current');
        expect(load).toHaveBeenCalledTimes(2);
    });

    it('does not retry from a superseded caller after the current read fails', async () => {
        const cache = new QueryCache();
        const old = deferred<string>();
        const failure = new Error('current read failed');
        const load = vi.fn().mockImplementationOnce(() => old.promise).mockRejectedValue(failure);
        const first = cache.read('Read', [], load);
        await Promise.resolve();
        cache.invalidate(['Read']);
        await expect(cache.read('Read', [], load)).rejects.toBe(failure);
        old.resolve('withdrawn');
        await expect(first).rejects.toBe(failure);
        expect(load).toHaveBeenCalledTimes(2);
    });

    it('settles concurrent argument reads when a scoped refresh changes the binding revision', async () => {
        const cache = new QueryCache();
        const older = deferred<string>();
        const firstLoad = vi.fn().mockImplementationOnce(() => older.promise).mockResolvedValue('current A');
        const first = cache.read('Read', ['A'], firstLoad);
        await Promise.resolve();
        await cache.read('Read', ['B'], async () => 'old B');
        cache.invalidate(['Read'], false, ['B']);
        await cache.read('Read', ['B'], async () => 'current B');
        older.resolve('older A');
        expect(await first).toBe('current A');
        expect(cache.peek('Read', ['A'])).toBe('current A');
        expect(cache.peek('Read', ['B'])).toBe('current B');
    });

    it('retains stale success on a background failure and retries the next read', async () => {
        let now = 0;
        const cache = new QueryCache(64, () => now);
        await cache.read('Read', [], async () => 'last success');
        now = 61_000;
        expect(await cache.read('Read', [], async () => { throw new Error('offline'); })).toBe('last success');
        await cache.state('Read')?.pending?.catch(() => {});
        expect(cache.peek('Read')).toBe('last success');
        await cache.read('Read', [], async () => 'recovered');
        await cache.state('Read')?.pending;
        expect(cache.peek('Read')).toBe('recovered');
    });

    it('evicts the least recently read entry above 64 and retains at most two stream pages', async () => {
        const cache = new QueryCache();
        for (let i = 0; i < 64; i++) await cache.read('Read', [i], async () => i);
        await cache.read('Read', [0], async () => -1);
        await cache.read('Read', [64], async () => 64);
        expect(cache.size).toBe(64);
        expect(cache.peek('Read', [0])).toBe(0);
        expect(cache.peek('Read', [1])).toBeUndefined();
        await cache.read('LoadStreamItemsPage', ['main', -1, 50], async () => ({ nextCursor: 100 }));
        await cache.read('LoadStreamItemsPage', ['main', 100, 50], async () => ({ nextCursor: 50 }));
        await cache.read('LoadStreamItemsPage', ['main', 50, 50], async () => ({ nextCursor: 0 }));
        expect(cache.peek('LoadStreamItemsPage', ['main', -1, 50])).toEqual({ nextCursor: 100 });
        expect(cache.peek('LoadStreamItemsPage', ['main', 100, 50])).toEqual({ nextCursor: 50 });
        expect(cache.peek('LoadStreamItemsPage', ['main', 50, 50])).toBeUndefined();
        expect(cache.size).toBeLessThanOrEqual(64);
    });

    it('runs one backend call at a time while events supersede a slow read', async () => {
        const cache = new QueryCache(64, () => 0);
        let inFlight = 0;
        let peak = 0;
        const gates: Array<() => void> = [];
        const load = vi.fn(() => new Promise<number>((resolve) => {
            inFlight += 1;
            peak = Math.max(peak, inFlight);
            gates.push(() => { inFlight -= 1; resolve(inFlight); });
        }));
        void cache.read('LoadStreamItemsPage', ['main', -1, 50], load);
        for (let event = 0; event < 6; event += 1) {
            cache.revalidate(['LoadStreamItemsPage']);
            await Promise.resolve();
        }
        // Each release lets the next queued call start; no two calls overlap.
        for (let step = 0; step < 20 && gates.length; step += 1) {
            gates.shift()!();
            await new Promise((resolve) => setTimeout(resolve, 0));
        }
        expect(peak).toBe(1);
    });
});
