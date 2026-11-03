import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { resetPagePrivacyWithdrawal } from './page-privacy-withdrawal';
import { useMainChatWaiting, resetMainChatWaitingStore } from './main-chat-waiting-store';

type WaitingState = {
    sessionId: string;
    day: string;
    count: number;
    conversations: number;
    finished: number;
    routine: number;
    items: unknown[];
    revision: number;
    ready: boolean;
};
const fixture = vi.hoisted(() => ({
    getter: vi.fn(),
    events: new Map<string, Set<(...args: unknown[]) => void>>(),
}));

vi.mock('@/lib/wails-shim-app', () => ({ GetMainChatWaiting: fixture.getter }));
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, handler: (...args: unknown[]) => void) => {
        const set = fixture.events.get(name) ?? new Set();
        set.add(handler);
        fixture.events.set(name, set);
        return () => set.delete(handler);
    },
}));

const state = (count: number, revision: number): WaitingState => ({
    sessionId: 'main', day: '2026-10-27', count, conversations: 1, finished: 2, routine: 3,
    items: [], revision, ready: true,
});

async function emit(name: string, payload?: unknown) {
    await act(async () => {
        for (const handler of fixture.events.get(name) ?? []) handler(payload);
    });
}

beforeEach(() => {
    resetMainChatWaitingStore();
    resetPagePrivacyWithdrawal();
    fixture.events.clear();
    fixture.getter.mockReset();
});
afterEach(() => {
    cleanup();
    resetMainChatWaitingStore();
    resetPagePrivacyWithdrawal();
});

it('accepts waiting events and ignores older revisions', async () => {
    fixture.getter.mockResolvedValue(state(2, 1));
    const { result } = renderHook(() => useMainChatWaiting());
    await waitFor(() => expect(result.current).toMatchObject({
        count: 2, day: '2026-10-27', conversations: 1, finished: 2, routine: 3, revision: 1, ready: true,
    }));

    await emit('main-chat:waiting-changed', state(3, 2));
    expect(result.current).toMatchObject({ count: 3, conversations: 1, finished: 2, routine: 3, revision: 2 });
    await emit('main-chat:waiting-changed', state(9, 1));
    expect(result.current.count).toBe(3);
});

it('clears synchronously on privacy withdrawal and fences the pending getter', async () => {
    fixture.getter.mockResolvedValueOnce(state(5, 1));
    let finishRefresh: ((value: WaitingState) => void) | undefined;
    fixture.getter.mockImplementationOnce(() => new Promise<WaitingState>((resolve) => { finishRefresh = resolve; }));
    const { result } = renderHook(() => useMainChatWaiting());
    await waitFor(() => expect(result.current.count).toBe(5));

    await emit('sensitive-apps:updated');
    expect(result.current).toMatchObject({ count: 0, items: [], ready: false });
    await emit('main-chat:waiting-changed', state(8, 2));
    expect(result.current.count).toBe(0);

    await act(async () => finishRefresh?.(state(4, 2)));
    expect(result.current).toMatchObject({ count: 4, revision: 2, ready: true });
});
