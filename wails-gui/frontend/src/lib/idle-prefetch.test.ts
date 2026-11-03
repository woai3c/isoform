import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { prefetchPage, prefetchSessionStream, resetPageQueries } from './page-queries';
import { pageQueryCache } from './query-cache';
import { prefetchPaused, resetIdlePrefetch, setPrefetchStreaming, startIdlePrefetch } from './idle-prefetch';

const fixture = vi.hoisted(() => ({ bindings: new Map<string, ReturnType<typeof vi.fn>>(), calls: [] as string[] }));
vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        if (!fixture.bindings.has(name)) {
            fixture.bindings.set(name, vi.fn(async () => {
                fixture.calls.push(name);
                if (name === 'GetMainChat') return { session: { localId: 'main' } };
                return [];
            }));
        }
        return fixture.bindings.get(name);
    },
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {} }));

/** Runs idle callbacks in order, one per flush(). */
function idleQueue() {
    const queue: Array<() => void> = [];
    return {
        onIdle: (run: () => void) => queue.push(run),
        flush: () => { queue.shift()?.(); },
        get size() { return queue.length; },
    };
}

async function flushMicrotimes() {
    for (let i = 0; i < 8; i++) await Promise.resolve();
}

async function drain(idle: ReturnType<typeof idleQueue>, done: Promise<void>) {
    let finished = false;
    void done.then(() => { finished = true; });
    for (let i = 0; i < 400 && !finished; i++) {
        idle.flush();
        await flushMicrotimes();
    }
    expect(finished).toBe(true);
}

/** The binding-call order the canonical page order produces on the wire.
 * Reads shared across pages (GetMemoryEntries, GetKnowledgeEntries) hit the
 * fresh cache the second time and never re-call; the chat session id chain
 * reads GetMainChat from the warm cache too, and the self-person step finds
 * no self in the empty principals fixture. */
const EXPECTED_ORDER = [
    'GetMainChat',
    'LoadStreamItemsPage',
    'EnsureDailySummary',
    'GetActivitiesInRange',
    'GetDayThreads',
    'GetCronRunsByDateRange',
    'GetKnowledgeEntries',
    'GetMemoryEntries',
    'GetRecentMeetingNotes',
    'GetDayNoticed',
    'GetAboutYou',
    'GetMemoryStatus',
    'GetAboutYouPortrait',
    'ListKnowHow',
    'GetKnowHowMatch',
    'ListPeopleOverview',
    'ListPrincipalsWithStats',
    'GetContactsAccessStatus',
    'GetSettingsOverview',
    'GetMobileBriefSchedule',
    'GetPlatformPermissions',
    'GetBackgroundListening',
    'GetAutomaticMeetingRecordingEnabled',
    'GetTextChannelStatus',
    'GetSMSChannelStatus',
    'LoadUIState',
    'LoadUIState',
    'LoadUIState',
    'LoadUIState',
    'ListGoals',
    'GetTodayTopics',
    'ListHandoffs',
];

beforeEach(() => {
    resetPageQueries();
    resetIdlePrefetch();
    fixture.calls = [];
    fixture.bindings.clear();
});
afterEach(() => {
    resetPageQueries();
    resetIdlePrefetch();
    vi.useRealTimers();
    vi.restoreAllMocks();
});

describe('idle prefetch', () => {
    it('warms every page one query at a time in the canonical order', async () => {
        const { pagePrefetchSteps } = await import('./page-queries');
        const steps = pagePrefetchSteps();
        // Every read hangs until the test releases it: an overlap in the walk
        // would show as a second call arriving before the first release.
        const releases = new Map<string, Array<() => void>>();
        const hanging = (name: string) => vi.fn(() => {
            fixture.calls.push(name);
            return new Promise((resolve) => {
                const list = releases.get(name) ?? [];
                list.push(() => resolve(name === 'GetMainChat' ? { session: { localId: 'main' } } : []));
                releases.set(name, list);
            });
        });
        for (const name of new Set(EXPECTED_ORDER)) fixture.bindings.set(name, hanging(name));

        // Two steps re-read a name the Activity steps already warmed: fresh
        // cache, no wire call, so the walk has silent steps. The self-person
        // step also stays silent here: the principals fixture has no self.
        // Snappy 1.0: the chat warms one page, its last 50 messages, and no
        // overview (the waiting count is in memory since #295).
        const silent = new Set([11, 15, 20]);
        expect(steps[11].name).toBe('GetMemoryEntries');
        expect(steps[15].name).toBe('GetKnowledgeEntries');
        expect(steps[20].name).toBe('GetPersonDetail');
        expect(steps.length).toBe(35);

        const idle = idleQueue();
        const done = startIdlePrefetch({ steps, onIdle: idle.onIdle });
        const releaseAll = (name: string) => {
            for (const release of releases.get(name) ?? []) release();
            releases.delete(name);
        };
        let wire = 0;
        for (let s = 0; s < steps.length; s++) {
            idle.flush();
            await flushMicrotimes();
            if (silent.has(s)) {
                expect(fixture.calls.length, `step ${s} stays off the wire`).toBe(wire);
                continue;
            }
            wire++;
            expect(fixture.calls, `step ${s} (${steps[s].name})`).toEqual(EXPECTED_ORDER.slice(0, wire));
            // One at a time: parking the current read parks the whole walk.
            idle.flush();
            await flushMicrotimes();
            expect(fixture.calls.length).toBe(wire);
            // The meetings step reads GetRecentMeetingNotes under the facade's
            // synthetic cache name.
            releaseAll(steps[s].name === 'Activity:meetings' ? 'GetRecentMeetingNotes' : steps[s].name);
            await flushMicrotimes();
        }
        await drain(idle, done);
        expect(fixture.calls).toEqual(EXPECTED_ORDER);
        // The chat stream pages read the just-warmed main session id.
        const stream = fixture.bindings.get('LoadStreamItemsPage')!;
        expect(stream.mock.calls).toEqual([['main', -1, 50]]);
    });

    it('pauses while the user types and resumes after the quiet window', async () => {
        vi.useFakeTimers();
        const idle = idleQueue();
        const done = startIdlePrefetch({ onIdle: idle.onIdle });
        idle.flush();
        // Park the walk before the first read settles and queues the next.
        const input = document.createElement('input');
        document.body.appendChild(input);
        input.dispatchEvent(new KeyboardEvent('keydown', { key: 'a', bubbles: true }));
        expect(prefetchPaused()).toBe(true);
        await vi.advanceTimersByTimeAsync(1);
        expect(fixture.calls).toEqual(['GetMainChat']);
        // Idle periods keep arriving while the user types; nothing starts.
        for (let i = 0; i < 5; i++) idle.flush();
        await vi.advanceTimersByTimeAsync(100);
        expect(fixture.calls).toEqual(['GetMainChat']);
        // The walk resumes once the quiet window passes.
        await vi.advanceTimersByTimeAsync(2500);
        idle.flush();
        await vi.advanceTimersByTimeAsync(1);
        idle.flush();
        await vi.advanceTimersByTimeAsync(1);
        expect(fixture.calls.length).toBeGreaterThan(1);
        input.remove();
        let finished = false;
        void done.then(() => { finished = true; });
        for (let i = 0; i < 400 && !finished; i++) {
            idle.flush();
            await vi.advanceTimersByTimeAsync(1);
        }
        expect(finished).toBe(true);
    });

    it('pauses while a chat reply streams', async () => {
        const idle = idleQueue();
        const done = startIdlePrefetch({ onIdle: idle.onIdle });
        idle.flush();
        // Park the walk before the first read settles and queues the next.
        setPrefetchStreaming(true);
        await flushMicrotimes();
        expect(fixture.calls).toEqual(['GetMainChat']);
        for (let i = 0; i < 5; i++) idle.flush();
        await flushMicrotimes();
        expect(fixture.calls).toEqual(['GetMainChat']);
        setPrefetchStreaming(false);
        idle.flush();
        await flushMicrotimes();
        idle.flush();
        await flushMicrotimes();
        expect(fixture.calls.length).toBeGreaterThan(1);
        await drain(idle, done);
    });

    it('runs once per session; a second start joins the first walk', async () => {
        const idle = idleQueue();
        const first = startIdlePrefetch({ onIdle: idle.onIdle });
        const second = startIdlePrefetch({ onIdle: idle.onIdle });
        expect(second).toBe(first);
        await drain(idle, first);
        const calls = fixture.calls.length;
        await startIdlePrefetch({ onIdle: idle.onIdle });
        expect(fixture.calls.length).toBe(calls);
    });

    it('uses requestIdleCallback when the platform has it', async () => {
        const idleSpy = vi.fn((run: () => void) => { run(); return 1; });
        vi.stubGlobal('requestIdleCallback', idleSpy);
        const done = startIdlePrefetch();
        let finished = false;
        void done.then(() => { finished = true; });
        for (let i = 0; i < 400 && !finished; i++) await flushMicrotimes();
        expect(finished).toBe(true);
        expect(idleSpy).toHaveBeenCalled();
    });

    it('parks the setTimeout fallback on a bounded timer instead of spinning zero-delay timers', async () => {
        // No requestIdleCallback and no custom scheduler: the WebKit path.
        vi.stubGlobal('requestIdleCallback', undefined);
        vi.useFakeTimers();
        fixture.bindings.set('ListGoals', vi.fn(async () => { fixture.calls.push('ListGoals'); return []; }));
        const steps = [{ page: 'goals', name: 'ListGoals', run: () => fixture.bindings.get('ListGoals')!() }];
        setPrefetchStreaming(true);
        const done = startIdlePrefetch({ steps });
        await vi.advanceTimersByTimeAsync(0);
        // Parked: one bounded timer waits, no work runs, and advancing time
        // re-arms it only once per window (500 ms) — never a zero-delay spin.
        expect(fixture.calls).toEqual([]);
        expect(vi.getTimerCount()).toBe(1);
        await vi.advanceTimersByTimeAsync(10_000);
        expect(fixture.calls).toEqual([]);
        expect(vi.getTimerCount()).toBe(1);
        // The walk resumes once the pause lifts.
        setPrefetchStreaming(false);
        await vi.advanceTimersByTimeAsync(501);
        await vi.advanceTimersByTimeAsync(1);
        expect(fixture.calls).toEqual(['ListGoals']);
        await expect(done).resolves.toBeUndefined();
    });
});

describe('hover and focus prefetch', () => {
    it('starts a page\'s primary reads and cancels nothing', async () => {
        prefetchPage('goals');
        await flushMicrotimes();
        expect(fixture.calls).toEqual(['ListGoals']);
        prefetchPage('people');
        await flushMicrotimes();
        expect(fixture.calls).toEqual(['ListGoals', 'ListPeopleOverview', 'ListPrincipalsWithStats', 'GetContactsAccessStatus']);
        // A second hover while fresh skips the wire.
        prefetchPage('goals');
        await flushMicrotimes();
        expect(fixture.calls).toEqual(['ListGoals', 'ListPeopleOverview', 'ListPrincipalsWithStats', 'GetContactsAccessStatus']);
        expect(pageQueryCache.peek('ListGoals')).toEqual([]);
    });

    it('warms a pinned session\'s first stream pages', async () => {
        prefetchSessionStream('session-9');
        await flushMicrotimes();
        const stream = fixture.bindings.get('LoadStreamItemsPage')!;
        expect(stream.mock.calls).toEqual([['session-9', -1, 50]]);
    });

    it('warms search recents and the settings overview', async () => {
        prefetchPage('search');
        prefetchPage('settings');
        await flushMicrotimes();
        expect(fixture.calls).toEqual(expect.arrayContaining(['GetTodayTopics', 'ListHandoffs', 'GetSettingsOverview']));
    });
});
