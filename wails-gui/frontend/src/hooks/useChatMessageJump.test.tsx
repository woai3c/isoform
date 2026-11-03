import { act, cleanup, render } from '@testing-library/react';
import { useCallback, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Project, Session } from '@/types';

const bindings = vi.hoisted(() => ({
    FindStreamItemCursor: vi.fn(),
    LoadStreamItemsPage: vi.fn(),
}));
vi.mock('@/lib/wails-shim-app', () => bindings);
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn(() => () => {}) }));

import { JUMP_PAGE_LIMIT, olderPageThrough, useChatMessageJump } from './useChatMessageJump';

describe('olderPageThrough', () => {
    it('loads from the oldest loaded row down past the message, with no gap', () => {
        // Item at sort order 40 (cursor 41); the chat holds rows from 120 on.
        expect(olderPageThrough({ nextCursor: 120, hasMore: true }, 41)).toEqual({ before: 120, limit: 120 - 41 + 1 + 10 });
    });

    it('asks for one bounded page at a time for an old message', () => {
        expect(olderPageThrough({ nextCursor: 10_000, hasMore: true }, 5)).toEqual({ before: 10_000, limit: JUMP_PAGE_LIMIT });
    });

    it('loads nothing when the message should already be loaded or nothing is older', () => {
        expect(olderPageThrough({ nextCursor: 120, hasMore: true }, 130)).toBeNull();
        expect(olderPageThrough({ nextCursor: 120, hasMore: false }, 41)).toBeNull();
    });
});

function chat(localId: string, extra: Partial<Session> = {}): Session {
    return {
        localId, label: localId, kind: localId === 'main' ? 'main' : 'side', items: [{ id: `${localId}-latest`, kind: 'assistant', content: 'latest', sortOrder: 900 }],
        itemsLoaded: true, itemsLoading: false, loadingMore: false, hasMore: true, nextCursor: 900, streamItemCount: 1, updatedAt: 1,
        ...extra,
    } as unknown as Session;
}

const harness = {
    api: null as null | ReturnType<typeof useChatMessageJump>,
    open: null as null | ((id: string) => void),
    failed: vi.fn(),
};

function Harness() {
    const [projects, setProjects] = useState<Project[]>([{
        id: 'p', name: 'p', workspaces: [{ id: 'w', sessions: [chat('main'), chat('other')] }],
    } as unknown as Project]);
    const [activeId, setActiveId] = useState('other');
    const updateProject = useCallback((id: string, update: (project: Project) => Project) => {
        setProjects((current) => current.map((project) => (project.id === id ? update(project) : project)));
    }, []);
    const activeSession = projects[0].workspaces[0].sessions.find((session) => session.localId === activeId);
    harness.open = setActiveId;
    harness.api = useChatMessageJump({
        projects,
        activeSession,
        updateProject,
        openSession: (_projectId, sessionId) => setActiveId(sessionId),
        onFailed: harness.failed,
    });
    return <output data-testid="items">{activeSession?.items.map((item) => item.id).join(',')}</output>;
}

async function settle() {
    for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
}

beforeEach(() => {
    bindings.FindStreamItemCursor.mockReset();
    bindings.LoadStreamItemsPage.mockReset();
    harness.failed.mockReset();
});
afterEach(cleanup);

describe('useChatMessageJump', () => {
    it('pages back in bounded loads until the message is in, then focuses it', async () => {
        bindings.FindStreamItemCursor.mockResolvedValue(101);
        bindings.LoadStreamItemsPage.mockImplementation(async (_id: string, before: number, limit: number) => {
            expect(limit).toBeLessThanOrEqual(JUMP_PAGE_LIMIT);
            const from = Math.max(before - limit, 0);
            const items = Array.from({ length: before - from }, (_, i) => ({ id: `m${from + i}`, kind: 'user', content: '', sortOrder: from + i }));
            return { items, hasMore: from > 0, nextCursor: from };
        });
        render(<Harness />);
        await act(async () => { await harness.api!.jump('main', 'm100'); });
        await settle();
        // 900 -> 700 -> 500 -> 300 -> 100: no gap, every load bounded, and
        // the fourth page holds m100.
        expect(bindings.LoadStreamItemsPage.mock.calls.map((call) => call[1])).toEqual([900, 700, 500, 300]);
        expect(harness.api!.focus).toMatchObject({ sessionId: 'main', itemId: 'm100' });
        expect(harness.failed).not.toHaveBeenCalled();
    });

    it('says so when the message is gone', async () => {
        bindings.FindStreamItemCursor.mockRejectedValue(new Error('SEARCH_STREAM_ITEM_NOT_FOUND'));
        render(<Harness />);
        await act(async () => { await harness.api!.jump('main', 'gone'); });
        expect(harness.failed).toHaveBeenCalledTimes(1);
        expect(harness.api!.focus).toBeNull();
    });

    it('drops the jump when the person opens another chat first', async () => {
        let answer: (cursor: number) => void = () => {};
        bindings.FindStreamItemCursor.mockImplementation(() => new Promise<number>((resolve) => { answer = resolve; }));
        bindings.LoadStreamItemsPage.mockResolvedValue({ items: [{ id: 'm5', kind: 'user', content: '', sortOrder: 5 }], hasMore: false, nextCursor: 5 });
        render(<Harness />);
        let pending: Promise<void> = Promise.resolve();
        await act(async () => { pending = harness.api!.jump('main', 'm5'); });
        // The main chat opened; the person goes to another chat before the
        // cursor answers.
        await act(async () => { harness.open!('other'); });
        await act(async () => { answer(6); await pending; });
        await act(async () => { harness.open!('main'); });
        await settle();
        expect(bindings.LoadStreamItemsPage).not.toHaveBeenCalled();
        expect(harness.api!.focus).toBeNull();
    });

    it('a newer jump replaces one in flight', async () => {
        const answers: Array<(cursor: number) => void> = [];
        bindings.FindStreamItemCursor.mockImplementation(() => new Promise<number>((resolve) => { answers.push(resolve); }));
        bindings.LoadStreamItemsPage.mockResolvedValue({ items: [], hasMore: false, nextCursor: -1 });
        render(<Harness />);
        let first: Promise<void> = Promise.resolve();
        let second: Promise<void> = Promise.resolve();
        await act(async () => { first = harness.api!.jump('main', 'old'); });
        await act(async () => { second = harness.api!.jump('main', 'main-latest'); });
        await act(async () => { answers[1](901); await second; });
        await act(async () => { answers[0](3); await first; });
        await settle();
        expect(harness.api!.focus).toMatchObject({ itemId: 'main-latest' });
        expect(bindings.LoadStreamItemsPage).not.toHaveBeenCalled();
        expect(harness.failed).not.toHaveBeenCalled();
    });
});
