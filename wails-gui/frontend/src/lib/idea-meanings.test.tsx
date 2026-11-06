import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// Invented titles and answers: the hook's behaviour is what is under test.
const mocks = vi.hoisted(() => ({
    groups: vi.fn(),
    listeners: new Map<string, (...args: unknown[]) => void>(),
}));

vi.mock('@/lib/wails-shim-app', () => ({
    IdeaMeaningGroups: (...args: unknown[]) => mocks.groups(...args),
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, callback: (...args: unknown[]) => void) => {
        mocks.listeners.set(name, callback);
        return () => { mocks.listeners.delete(name); };
    },
}));

import { useIdeaMeanings } from './idea-meanings';

const ITEMS = [
    { title: 'Keep release notes short', savedAt: 1 },
    { title: 'Keep the notes brief', savedAt: 2 },
];
const titleOf = (item: { title: string }) => item.title;
const savedAtOf = (item: { savedAt: number }) => item.savedAt;

async function flush() {
    await act(async () => { await Promise.resolve(); });
}

describe('useIdeaMeanings re-reads on an event, never on a timer (R24)', () => {
    beforeEach(() => {
        mocks.groups.mockReset();
        mocks.listeners.clear();
        vi.useFakeTimers();
    });
    afterEach(() => {
        vi.useRealTimers();
    });

    it('makes no timed calls while the groups are pending', async () => {
        mocks.groups.mockResolvedValue({ groups: null, pending: true });
        renderHook(() => useIdeaMeanings(ITEMS, titleOf, savedAtOf, 0.75));
        await flush();
        expect(mocks.groups).toHaveBeenCalledTimes(1);
        await act(async () => { vi.advanceTimersByTime(60 * 60 * 1000); });
        await flush();
        expect(mocks.groups).toHaveBeenCalledTimes(1);
    });

    it('re-reads once when the ready event arrives, then delivers the groups', async () => {
        mocks.groups
            .mockResolvedValueOnce({ groups: null, pending: true })
            .mockResolvedValueOnce({ groups: [0, 0], pending: false });
        const { result } = renderHook(() => useIdeaMeanings(ITEMS, titleOf, savedAtOf, 0.75));
        await flush();
        expect(result.current).toBeNull();
        await act(async () => { mocks.listeners.get('idea-meanings:ready')?.(); });
        await flush();
        expect(mocks.groups).toHaveBeenCalledTimes(2);
        expect(result.current).toEqual([0, 0]);
    });

    it('ignores a ready event when nothing is waiting', async () => {
        mocks.groups.mockResolvedValue({ groups: [0, 1], pending: false });
        renderHook(() => useIdeaMeanings(ITEMS, titleOf, savedAtOf, 0.75));
        await flush();
        await act(async () => { mocks.listeners.get('idea-meanings:ready')?.(); });
        await flush();
        expect(mocks.groups).toHaveBeenCalledTimes(1);
    });

    it('stops listening when it unmounts', async () => {
        mocks.groups.mockResolvedValue({ groups: null, pending: true });
        const { unmount } = renderHook(() => useIdeaMeanings(ITEMS, titleOf, savedAtOf, 0.75));
        await flush();
        unmount();
        expect(mocks.listeners.has('idea-meanings:ready')).toBe(false);
    });
});
