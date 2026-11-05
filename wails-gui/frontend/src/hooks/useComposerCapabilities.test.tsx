import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import '@/i18n';
import { resetAppActivity } from '@/lib/app-activity';
import { useComposerCapabilities } from './useComposerCapabilities';

const fixture = vi.hoisted(() => ({ snapshot: vi.fn() }));

vi.mock('@/lib/wails-shim-app', () => ({
    GetComputerUseAppAccessSnapshot: fixture.snapshot,
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: () => () => undefined,
}));

function setHidden(hidden: boolean) {
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => (hidden ? 'hidden' : 'visible') });
    document.dispatchEvent(new Event('visibilitychange'));
}

describe('useComposerCapabilities', () => {
    beforeEach(() => {
        vi.useFakeTimers();
        resetAppActivity();
        fixture.snapshot.mockReset().mockResolvedValue({ available: true, accessibility: true, screenLocked: false });
        setHidden(false);
    });
    afterEach(() => {
        vi.useRealTimers();
        setHidden(false);
    });

    it('makes no calls while hidden, and one read when shown again', async () => {
        setHidden(true);
        const { unmount } = renderHook(() => useComposerCapabilities());
        await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
        expect(fixture.snapshot).not.toHaveBeenCalled();

        setHidden(false);
        await act(async () => { await vi.advanceTimersByTimeAsync(0); });
        const afterShown = fixture.snapshot.mock.calls.length;
        expect(afterShown).toBe(1);

        await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
        expect(fixture.snapshot.mock.calls.length).toBe(afterShown + 1);
        unmount();
    });

    it('stops polling while the window is hidden', async () => {
        const { unmount } = renderHook(() => useComposerCapabilities());
        await act(async () => { await vi.advanceTimersByTimeAsync(0); });
        const before = fixture.snapshot.mock.calls.length;
        setHidden(true);
        await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
        expect(fixture.snapshot.mock.calls.length).toBe(before);
        unmount();
    });
});
