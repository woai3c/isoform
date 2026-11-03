import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { usePagePrefetch } from './usePagePrefetch';
const warm = vi.hoisted(() => ({ chat: vi.fn().mockResolvedValue(undefined), today: vi.fn().mockResolvedValue(undefined) }));
vi.mock('@/lib/page-queries', () => ({ prefetchMainChat: warm.chat, prefetchToday: warm.today }));
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.clearAllMocks(); });

it('waits for content and the next idle callback, then warms each direction only once', async () => {
    let frame!: FrameRequestCallback;
    let idle!: IdleRequestCallback;
    vi.stubGlobal('requestAnimationFrame', vi.fn((cb) => { frame = cb; return 1; }));
    vi.stubGlobal('cancelAnimationFrame', vi.fn());
    vi.stubGlobal('requestIdleCallback', vi.fn((cb) => { idle = cb; return 2; }));
    vi.stubGlobal('cancelIdleCallback', vi.fn());
    const hook = renderHook(({ page, ready }) => usePagePrefetch(page, ready), { initialProps: { page: 'today', ready: false } });
    expect(requestAnimationFrame).not.toHaveBeenCalled();
    hook.rerender({ page: 'today', ready: true });
    expect(warm.chat).not.toHaveBeenCalled();
    act(() => frame(0));
    expect(requestIdleCallback).toHaveBeenCalledTimes(1);
    await act(async () => idle({ didTimeout: false, timeRemaining: () => 30 }));
    expect(warm.chat).toHaveBeenCalledTimes(1);
    hook.rerender({ page: 'chat', ready: true });
    act(() => frame(16));
    await act(async () => idle({ didTimeout: false, timeRemaining: () => 30 }));
    expect(warm.today).toHaveBeenCalledTimes(1);
    for (let i = 0; i < 3; i++) {
        hook.rerender({ page: 'today', ready: true });
        hook.rerender({ page: 'chat', ready: true });
    }
    expect(warm.chat).toHaveBeenCalledTimes(1);
    expect(warm.today).toHaveBeenCalledTimes(1);
});

it('cancels work when the page is left before it paints', () => {
    vi.stubGlobal('requestAnimationFrame', vi.fn(() => 7));
    vi.stubGlobal('cancelAnimationFrame', vi.fn());
    const hook = renderHook(() => usePagePrefetch('today', true));
    hook.unmount();
    expect(cancelAnimationFrame).toHaveBeenCalledWith(7);
    expect(warm.chat).not.toHaveBeenCalled();
});
