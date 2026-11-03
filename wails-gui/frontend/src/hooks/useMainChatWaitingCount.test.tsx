import { cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { useMainChatWaitingCount } from './useMainChatWaitingCount';

const fixture = vi.hoisted(() => ({
    state: { sessionId: '', count: 0, items: [], revision: 0, ready: false },
}));

vi.mock('@/lib/main-chat-waiting-store', () => ({
    useMainChatWaiting: () => fixture.state,
}));
vi.mock('@/lib/launch-perf', () => ({ markLaunchPaint: vi.fn() }));

beforeEach(() => {
    fixture.state = { sessionId: '', count: 0, items: [], revision: 0, ready: false };
});
afterEach(cleanup);

it('returns null until the shared Go state is ready, then returns its count', () => {
    const { result, rerender } = renderHook(() => useMainChatWaitingCount());
    expect(result.current).toBeNull();

    fixture.state = { sessionId: 'main', count: 4, items: [], revision: 2, ready: true };
    rerender();
    expect(result.current).toBe(4);
});
