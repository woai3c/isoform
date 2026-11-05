import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { resetPageQueries } from '@/lib/page-queries';
import { useChangeDrivenRead, CHANGE_READ_COALESCE_MS } from './useChangeDrivenRead';

const fixture = vi.hoisted(() => ({
    listeners: new Map<string, Set<(payload: unknown) => void>>(),
}));

vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, handler: (payload: unknown) => void) => {
        const listeners = fixture.listeners.get(name) ?? new Set();
        listeners.add(handler);
        fixture.listeners.set(name, listeners);
        return () => listeners.delete(handler);
    },
}));

function fire(name: string, data?: unknown) {
    for (const handler of fixture.listeners.get(name) ?? []) handler(data);
}
function setHidden(hidden: boolean) {
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => (hidden ? 'hidden' : 'visible') });
    document.dispatchEvent(new Event('visibilitychange'));
}
const wait = (ms: number) => act(async () => { await new Promise((resolve) => setTimeout(resolve, ms)); });

describe('useChangeDrivenRead', () => {
    beforeEach(() => {
        resetPageQueries();
        fixture.listeners.clear();
        setHidden(false);
    });

    it('reads once on mount and once per coalesced burst', async () => {
        const read = vi.fn().mockResolvedValue(undefined);
        renderHook(() => useChangeDrivenRead(true, ['x:changed'], read));
        await wait(CHANGE_READ_COALESCE_MS + 50);
        expect(read).toHaveBeenCalledTimes(1);
        await act(async () => { for (let i = 0; i < 30; i++) fire('x:changed'); });
        await wait(CHANGE_READ_COALESCE_MS + 50);
        expect(read).toHaveBeenCalledTimes(2);
    });

    it('starts no read while enabled is false', async () => {
        const read = vi.fn().mockResolvedValue(undefined);
        renderHook(() => useChangeDrivenRead(false, ['x:changed'], read));
        fire('x:changed');
        await wait(CHANGE_READ_COALESCE_MS + 50);
        expect(read).not.toHaveBeenCalled();
    });

    it('never reads while hidden and reads once when shown', async () => {
        const read = vi.fn().mockResolvedValue(undefined);
        setHidden(true);
        renderHook(() => useChangeDrivenRead(true, ['x:changed'], read));
        await wait(CHANGE_READ_COALESCE_MS + 50);
        await act(async () => { for (let i = 0; i < 10; i++) fire('x:changed'); });
        await wait(CHANGE_READ_COALESCE_MS + 50);
        expect(read).not.toHaveBeenCalled();
        setHidden(false);
        await wait(CHANGE_READ_COALESCE_MS + 50);
        expect(read).toHaveBeenCalledTimes(1);
    });

    it('queues exactly one follow-up when an event lands during a read', async () => {
        let release!: () => void;
        const read = vi.fn(() => new Promise<void>((resolve) => { release = resolve; }));
        renderHook(() => useChangeDrivenRead(true, ['x:changed'], read));
        await wait(CHANGE_READ_COALESCE_MS + 20);
        expect(read).toHaveBeenCalledTimes(1);
        await act(async () => { for (let i = 0; i < 5; i++) fire('x:changed'); });
        await wait(CHANGE_READ_COALESCE_MS + 20);
        expect(read).toHaveBeenCalledTimes(1);
        await act(async () => { release(); });
        await wait(CHANGE_READ_COALESCE_MS + 50);
        expect(read).toHaveBeenCalledTimes(2);
    });

    it('filters events by payload and stops listening on unmount', async () => {
        const read = vi.fn().mockResolvedValue(undefined);
        const { unmount } = renderHook(() => useChangeDrivenRead(true, ['x:changed'], read, (data) => data === 'mine'));
        await wait(CHANGE_READ_COALESCE_MS + 50);
        fire('x:changed', 'other');
        await wait(CHANGE_READ_COALESCE_MS + 50);
        expect(read).toHaveBeenCalledTimes(1);
        unmount();
        fire('x:changed', 'mine');
        await wait(CHANGE_READ_COALESCE_MS + 50);
        expect(read).toHaveBeenCalledTimes(1);
    });
});
