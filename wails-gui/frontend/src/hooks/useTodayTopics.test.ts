import { resetPageQueries } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SIDEBAR_TOPICS_CHANGED, TOPICS_UPDATED_EVENT, toTodayTopics, useSidebarTopics, useTodayTopics } from "./useTodayTopics";

const mocks = vi.hoisted(() => ({ get: vi.fn(), force: vi.fn(), sidebar: vi.fn(), listeners: new Map<string, Set<() => void>>() }));
vi.mock("@/lib/wails-shim-app", () => ({ GetTodayTopics: mocks.get, RefreshTodayTopics: mocks.force, ListSidebarTopics: mocks.sidebar }));
vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, handler: () => void) => {
        const set = mocks.listeners.get(name) ?? new Set();
        set.add(handler);
        mocks.listeners.set(name, set);
        return () => set.delete(handler);
    },
}));

/** The computer's background retry succeeded (#167). The shared change hook
 *  coalesces events for 250 ms, so the read lands after that window. */
async function emitTopicsUpdated() {
    await act(async () => {
        for (const handler of mocks.listeners.get(TOPICS_UPDATED_EVENT) ?? []) handler();
        await new Promise((resolve) => setTimeout(resolve, 300));
    });
}

const refreshFailed = () => new Error("topic_registry_refresh_failed: step=views kind=deadline");

beforeEach(() => {
    resetPageQueries();
    vi.clearAllMocks();
    mocks.listeners.clear();
});

describe("toTodayTopics (A1 TopicView)", () => {
    it("retains the newest find independently of the latest work moment", () => {
        expect(toTodayTopics([{ id: "a", kind: "project", name: "Halcyon", latest: { text: "Met today" }, latestFind: { text: "Q3 results" } }]))
            .toEqual([expect.objectContaining({ latest: { text: "Met today", at: undefined }, latestFind: { text: "Q3 results", at: undefined } })]);
    });
    it("reads the registry's fields and keeps topics with an id, a name and a known kind, in order", () => {
        expect(toTodayTopics([
            {
                id: "a", kind: "project", name: " Halcyon ", status: "waiting_on_you",
                latest: { text: "They want the sync moved up.", at: "2026-10-21T06:20:00Z" },
                next: { text: "standup", at: "2026-10-21T21:00:00Z" },
                peopleLine: { names: ["Dana", "Lee", "Kim", "Extra"], count: 6 }, pinnedToSidebar: true,
            },
            // newStories is the count the hub asked A1 for; sourceCounts alone is not read as one.
            { id: "b", kind: "following", name: "Wearable AI", status: "new_stories", newStories: 2, sourceCounts: { find: 5 }, why: { lastSeenAt: "2026-10-20T10:00:00Z" } },
            { id: "", kind: "project", name: "No id" },
            { id: "c", kind: "person", name: "Odd kind" },
            { id: "d", kind: "project", name: "Odd status", status: "blocked", latest: { text: "  " } },
        ])).toEqual([
            {
                id: "a", name: "Halcyon", kind: "work", status: "waiting", newCount: undefined,
                latest: { text: "They want the sync moved up.", at: "2026-10-21T06:20:00Z" },
                next: { text: "standup", at: "2026-10-21T21:00:00Z" },
                people: { names: ["Dana", "Lee", "Kim"], count: 6 }, lastSeenAt: undefined, pinned: true,
            },
            {
                id: "b", name: "Wearable AI", kind: "followed", status: "news", newCount: 2,
                latest: undefined, next: undefined, people: undefined, lastSeenAt: "2026-10-20T10:00:00Z", pinned: false,
            },
            {
                id: "d", name: "Odd status", kind: "work", status: undefined, newCount: undefined,
                latest: undefined, next: undefined, people: undefined, lastSeenAt: undefined, pinned: false,
            },
        ]);
        expect(toTodayTopics(null)).toEqual([]);
        expect(toTodayTopics({ topics: [{ id: "x", kind: "project", name: "Wrapped" }] })).toHaveLength(1);
    });

    it("maps a sent topic to waiting on them", () => {
        expect(toTodayTopics([{ id: "a", kind: "project", name: "Halcyon", status: "waiting_on_them" }]))
            .toEqual([expect.objectContaining({ id: "a", status: "waitingOnThem" })]);
    });

    it("keeps the followed topics the registry sends, even without a why under Closed Eyes", () => {
        // The registry keeps the why rule (item 61) and withholds observed
        // times under Closed Eyes; Today shows what it sends, with less.
        const topics = toTodayTopics([
            { id: "w", kind: "project", name: "Work" },
            { id: "f1", kind: "following", name: "With why", why: { lastSeenAt: "2026-10-20T10:00:00Z", interestWhy: "Building agents" } },
            { id: "f2", kind: "following", name: "Closed Eyes" },
        ]);
        expect(topics.map((topic) => [topic.id, topic.lastSeenAt])).toEqual([["w", undefined], ["f1", "2026-10-20T10:00:00Z"], ["f2", undefined]]);
    });
});

describe("useTodayTopics", () => {
    it("shows no topics and no error before the registry exists", async () => {
        mocks.get.mockRejectedValue(new Error("Wails binding GetTodayTopics is not available"));
        const { result } = renderHook(() => useTodayTopics(true));
        await waitFor(() => expect(result.current.status).toBe("unavailable"));
        expect(result.current.topics).toEqual([]);
    });

    it("keeps the last topics when a later read fails", async () => {
        mocks.get.mockResolvedValueOnce([{ id: "a", name: "Halcyon", kind: "project" }]);
        const { result } = renderHook(() => useTodayTopics(true));
        await waitFor(() => expect(result.current.topics).toHaveLength(1));
        mocks.get.mockRejectedValueOnce(new Error("read failed"));
        await act(async () => { await result.current.refresh(); });
        expect(result.current.status).toBe("ready");
        expect(result.current.topics).toHaveLength(1);
    });
});

describe("useTodayTopics and topics:updated", () => {
    it("keeps topics through an empty refreshing result and replaces them when the build finishes", async () => {
        mocks.get.mockResolvedValueOnce({ topics: [{ id: "a", name: "Halcyon", kind: "project" }], refreshing: true });
        const { result } = renderHook(() => useTodayTopics(true));
        await waitFor(() => expect(result.current.topics).toHaveLength(1));
        expect(result.current.refreshing).toBe(true);
        mocks.get.mockResolvedValueOnce({ topics: [], refreshing: true });
        await emitTopicsUpdated();
        expect(result.current.topics.map((topic) => topic.name)).toEqual(["Halcyon"]);
        expect(result.current.status).toBe("ready");
        mocks.get.mockResolvedValueOnce({ topics: [{ id: "b", name: "Full registry", kind: "project" }], refreshing: false });
        await emitTopicsUpdated();
        expect(result.current.topics.map((topic) => topic.name)).toEqual(["Full registry"]);
        expect(result.current.refreshing).toBe(false);
    });

    it("clears retained topics when the computer withdraws their source proof during a refresh", async () => {
        mocks.get.mockResolvedValueOnce({ topics: [{ id: "a", name: "Halcyon", kind: "project" }], refreshing: false });
        const { result } = renderHook(() => useTodayTopics(true));
        await waitFor(() => expect(result.current.topics).toHaveLength(1));
        mocks.get.mockResolvedValueOnce({ topics: [], refreshing: true, clear: true });
        await emitTopicsUpdated();
        expect(result.current.topics).toEqual([]);
        expect(result.current.refreshing).toBe(true);
    });

    it("accepts an empty cold projection without showing the generic error", async () => {
        mocks.get.mockResolvedValueOnce({ topics: [], refreshing: true });
        const { result } = renderHook(() => useTodayTopics(true));
        await waitFor(() => expect(result.current.status).toBe("ready"));
        expect(result.current.topics).toEqual([]);
        expect(result.current.refreshing).toBe(true);
    });

    it("recovers from a failed first read when the background retry succeeds", async () => {
        mocks.get.mockRejectedValueOnce(refreshFailed());
        const { result } = renderHook(() => useTodayTopics(true));
        await waitFor(() => expect(result.current.status).toBe("failed"));
        expect(result.current.topics).toEqual([]);
        mocks.get.mockResolvedValueOnce([{ id: "a", name: "Halcyon", kind: "project" }]);
        await emitTopicsUpdated();
        await waitFor(() => expect(result.current.status).toBe("ready"));
        expect(result.current.topics.map((topic) => topic.name)).toEqual(["Halcyon"]);
    });

    it("refreshes a topic to waiting on them after a send", async () => {
        mocks.get.mockResolvedValueOnce([{ id: "a", name: "Halcyon", kind: "project", status: "waiting_on_you" }]);
        const { result } = renderHook(() => useTodayTopics(true));
        await waitFor(() => expect(result.current.topics[0]?.status).toBe("waiting"));
        mocks.get.mockResolvedValueOnce([{ id: "a", name: "Halcyon", kind: "project", status: "waiting_on_them" }]);
        await emitTopicsUpdated();
        await waitFor(() => expect(result.current.topics[0]?.status).toBe("waitingOnThem"));
    });

    it("keeps the last good topics and status on topic_registry_refresh_failed", async () => {
        mocks.get.mockResolvedValueOnce([{ id: "a", name: "Halcyon", kind: "project" }]);
        const { result } = renderHook(() => useTodayTopics(true));
        await waitFor(() => expect(result.current.topics).toHaveLength(1));
        mocks.get.mockRejectedValueOnce(refreshFailed());
        await emitTopicsUpdated();
        expect(mocks.get).toHaveBeenCalledTimes(2);
        expect(result.current.status).toBe("ready");
        expect(result.current.refreshFailed).toBe(true);
        expect(result.current.lastUpdatedAt).not.toBeNull();
        expect(result.current.topics.map((topic) => topic.name)).toEqual(["Halcyon"]);
    });

    it("lets the latest read win when two overlap", async () => {
        let resolveOld: (value: unknown) => void = () => {};
        mocks.get.mockResolvedValueOnce([{ id: "a", name: "First", kind: "project" }]);
        const { result } = renderHook(() => useTodayTopics(true));
        await waitFor(() => expect(result.current.topics).toHaveLength(1));
        mocks.get.mockReturnValueOnce(new Promise((resolve) => { resolveOld = resolve; }));
        mocks.get.mockResolvedValueOnce([{ id: "b", name: "Newest", kind: "project" }]);
        await act(async () => { result.current.refresh(); });
        await emitTopicsUpdated();
        await act(async () => { resolveOld([{ id: "c", name: "Stale", kind: "project" }]); });
        expect(result.current.topics.map((topic) => topic.name)).toEqual(["Newest"]);
    });

    it("stops listening when it unmounts", async () => {
        mocks.get.mockResolvedValue([]);
        const { unmount } = renderHook(() => useTodayTopics(true));
        await waitFor(() => expect(mocks.get).toHaveBeenCalledTimes(1));
        unmount();
        // The renderer retains one invalidator while the page is unmounted.
        expect(mocks.listeners.get(TOPICS_UPDATED_EVENT)?.size).toBe(1);
        await emitTopicsUpdated();
        expect(mocks.get).toHaveBeenCalledTimes(1);
    });
});

describe("useSidebarTopics", () => {
    it("shows the pinned topics once the background retry succeeds, and keeps them on a later failure", async () => {
        mocks.sidebar.mockRejectedValueOnce(refreshFailed());
        const { result } = renderHook(() => useSidebarTopics());
        await act(async () => {});
        expect(result.current.topics).toEqual([]);
        mocks.sidebar.mockResolvedValueOnce([{ id: "a", name: "Halcyon", kind: "project", pinnedToSidebar: true }]);
        await emitTopicsUpdated();
        await waitFor(() => expect(result.current.topics.map((topic) => topic.name)).toEqual(["Halcyon"]));
        mocks.sidebar.mockRejectedValueOnce(refreshFailed());
        await emitTopicsUpdated();
        expect(mocks.sidebar).toHaveBeenCalledTimes(3);
        expect(result.current.topics.map((topic) => topic.name)).toEqual(["Halcyon"]);
    });

    it("stops listening when it unmounts", async () => {
        mocks.sidebar.mockResolvedValue([]);
        const { unmount } = renderHook(() => useSidebarTopics());
        await waitFor(() => expect(mocks.listeners.get(TOPICS_UPDATED_EVENT)?.size).toBe(1));
        unmount();
        expect(mocks.listeners.get(TOPICS_UPDATED_EVENT)?.size).toBe(0);
        await emitTopicsUpdated();
        expect(mocks.sidebar).toHaveBeenCalledTimes(1);
    });


    it("lists the pinned topics and reads again when a pin changes", async () => {
        mocks.sidebar.mockResolvedValueOnce([{ id: "a", name: "Halcyon", kind: "project", pinnedToSidebar: true }]);
        const { result } = renderHook(() => useSidebarTopics());
        await waitFor(() => expect(result.current.topics.map((topic) => topic.name)).toEqual(["Halcyon"]));
        mocks.sidebar.mockResolvedValueOnce([]);
        await act(async () => { window.dispatchEvent(new Event(SIDEBAR_TOPICS_CHANGED)); });
        await waitFor(() => expect(result.current.topics).toEqual([]));
    });

    it("shows none before the registry exists", async () => {
        mocks.sidebar.mockRejectedValue(new Error("Wails binding ListSidebarTopics is not available"));
        const { result } = renderHook(() => useSidebarTopics());
        await act(async () => {});
        expect(result.current.topics).toEqual([]);
    });
});

it('withholds redacted names and the main chat from every topic reader', () => {
    expect(toTodayTopics([
        { id: 'a', kind: 'work', name: '[REDACTED: name]' },
        { id: 'b', kind: 'work', name: '[REDACTE…' },
        { id: 'c', kind: 'work', name: 'Isoform Main chat' },
        { id: 'd', kind: 'work', name: 'Release' },
    ]).map(topic => topic.name)).toEqual(['Release']);
});

it.each(['[masked]', '[...]', 'Release [...]', '[REDACTED:account_number]'])('withholds the shared privacy mask in a registry name: %s', name => {
    expect(toTodayTopics([{ id: 'private', kind: 'work', name }])).toEqual([]);
});

it('clears a pinned label immediately on a committed source withdrawal', async () => {
    mocks.sidebar.mockResolvedValueOnce([{ id: 'a', name: 'Atlas', kind: 'project', pinnedToSidebar: true }]);
    const hook = renderHook(() => useSidebarTopics());
    await waitFor(() => expect(hook.result.current.topics).toHaveLength(1));
    let finish!: (value: unknown) => void;
    mocks.sidebar.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    await act(async () => { for (const handler of mocks.listeners.get('topics:withdrawn') ?? []) handler(); });
    expect(hook.result.current.topics).toEqual([]);
    await act(async () => finish([]));
    expect(hook.result.current.topics).toEqual([]);
});


it('only requests a full collection for an explicit refresh, preserving cards if dispatch fails', async () => {
    mocks.get.mockResolvedValue({ topics: [{ id: 'a', name: 'Atlas', kind: 'project' }], refreshing: false });
    mocks.force.mockResolvedValue(undefined);
    const hook = renderHook(() => useTodayTopics(true));
    await waitFor(() => expect(hook.result.current.topics).toHaveLength(1));
    await emitTopicsUpdated();
    await act(async () => window.dispatchEvent(new Event('focus')));
    expect(mocks.force).not.toHaveBeenCalled();
    await act(async () => { await hook.result.current.refresh(); });
    expect(mocks.force).toHaveBeenCalledTimes(1);
    mocks.force.mockRejectedValueOnce(new Error('dispatch failed'));
    await act(async () => { await hook.result.current.refresh(); });
    expect(hook.result.current.topics[0].name).toBe('Atlas');
    expect(hook.result.current.status).toBe('ready');
    expect(hook.result.current.refreshFailed).toBe(true);
});

it('keeps the latest publication when successive reads finish inside one React batch', async () => {
    const empty = { topics: [], refreshing: false, hasTopics: false };
    mocks.get.mockResolvedValue(empty);
    mocks.force.mockResolvedValue(undefined);
    const hook = renderHook(() => useTodayTopics(true));
    await waitFor(() => expect(hook.result.current.status).toBe('ready'));
    await act(async () => {
        mocks.get.mockResolvedValue({ topics: [{ id: 'a', name: 'Intermediate', kind: 'project' }], refreshing: false, hasTopics: true });
        await hook.result.current.refresh();
        mocks.get.mockResolvedValue(empty);
        await hook.result.current.refresh();
    });
    expect(hook.result.current.topics).toEqual([]);
    expect(hook.result.current.hasTopics).toBe(false);
});

it('seeds firstRun from the cache and preserves it after a failed read', async () => {
    pageQueryCache.set('GetTodayTopics', [], { topics: [], firstRun: true, refreshing: false });
    const cached = renderHook(() => useTodayTopics(false));
    expect(cached.result.current.firstRun).toBe(true);
    cached.unmount();

    resetPageQueries();
    mocks.get.mockResolvedValueOnce({ topics: [], firstRun: true, refreshing: false });
    const hook = renderHook(() => useTodayTopics(true));
    await waitFor(() => expect(hook.result.current.firstRun).toBe(true));
    mocks.get.mockRejectedValueOnce(refreshFailed());
    await emitTopicsUpdated();
    expect(hook.result.current.firstRun).toBe(true);
});


it('keeps an unchanged pinned list quiet but still accepts the backend pin order', async () => {
    const pins = [
        { id: 'a', kind: 'project', name: 'Atlas', pinnedToSidebar: true },
        { id: 'b', kind: 'project', name: 'Beacon', pinnedToSidebar: true },
    ];
    mocks.sidebar.mockImplementation(async () => structuredClone(pins));
    const hook = renderHook(() => useSidebarTopics());
    await waitFor(() => expect(hook.result.current.topics).toHaveLength(2));
    const original = hook.result.current.topics;
    await emitTopicsUpdated();
    expect(hook.result.current.topics).toBe(original);
    mocks.sidebar.mockResolvedValue([...pins].reverse());
    await emitTopicsUpdated();
    expect(hook.result.current.topics.map(topic => topic.id)).toEqual(['b', 'a']);
});
