import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { Profiler, useState, type ReactNode } from 'react';
import { expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { StreamItem } from '@/types';
import { TodayPage } from './today/TodayPage';
import { ActivityPage, type ActivitySectionId } from './activity-page';
import { MainChatStreamBody } from './main-chat/main-chat-stream-body';
import { useMainChatView } from '@/hooks/useMainChatView';
import { resetPageQueries } from '@/lib/page-queries';
import { resetActivitySessionCache } from './timeline/activity-session-cache';
import type { CronJobsProp } from './timeline/useCalendarData';
import { busyDayFixture, chatStreamFixture } from './render-speed.fixture';
import { highlightCacheProbe } from './markdown-content';

// Opt-in render fixture (RENDER_SPEED_FIXTURE=1): React Profiler commit times
// for the four measured journeys — opening Chat with a 500-row stream, a
// streamed-token flush, a composer keystroke, switching back to Today, and
// opening Activity with a 300-moment/20-thread day. jsdom commit time is not
// native WebView paint; it is the React half of "first change" latency and it
// is comparable across branches. Bindings resolve immediately: data waiting
// is the preload branch's fixture, not this one.
const fixture = vi.hoisted(() => ({ payload: {} as Record<string, unknown> }));
vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        return (..._args: unknown[]) => Promise.resolve(fixture.payload[name] ?? []);
    },
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {}, EventsEmit: vi.fn(), BrowserOpenURL: vi.fn(), ClipboardSetText: vi.fn() }));
vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));
// react-virtuoso renders nothing under jsdom. The windowed mock mounts the
// last row of the windowed old section — what the WebView mounts above the
// fully rendered recent section when the stream opens at the bottom (real
// rows are taller than the estimate, so overscan covers about one).
vi.mock('react-virtuoso', async () => {
    const { virtuosoMockModule } = await import('@/test-utils/virtuoso-mock');
    return virtuosoMockModule(1);
});

type Commit = { phase: string; actualDuration: number; baseDuration: number; at: number };

function makeProfiler(sink: Commit[]) {
    return function Profile({ children }: { children: ReactNode }) {
        return (
            <Profiler
                id="fixture"
                onRender={(_id, phase, actualDuration, baseDuration) => {
                    sink.push({ phase, actualDuration: Math.round(actualDuration * 10) / 10, baseDuration: Math.round(baseDuration * 10) / 10, at: Math.round(performance.now()) });
                }}
            >
                {children}
            </Profiler>
        );
    };
}

function report(scenario: string, wallMs: number, commits: Commit[]) {
    const mount = commits.filter((c) => c.phase === 'mount');
    const update = commits.filter((c) => c.phase === 'update');
    const sum = (list: Commit[]) => Math.round(list.reduce((s, c) => s + c.actualDuration, 0) * 10) / 10;
    const max = (list: Commit[]) => Math.round(Math.max(0, ...list.map((c) => c.actualDuration)) * 10) / 10;
    console.info('[render-fixture]', JSON.stringify({
        scenario,
        wallMs: Math.round(wallMs * 10) / 10,
        mountCommits: mount.length,
        updateCommits: update.length,
        mountActualMs: sum(mount),
        updateActualMs: sum(update),
        maxCommitMs: max(commits),
        commits,
    }));
}

const noop = () => {};

function ChatHarness({ pairs }: { pairs: number }) {
    const [items, setItems] = useState<StreamItem[]>(() => chatStreamFixture(pairs));
    const [prompt, setPrompt] = useState('');
    const view = useMainChatView({ enabled: true, items });
    return (
        <>
            <input aria-label="fixture-composer" value={prompt} onChange={(e) => setPrompt(e.target.value)} />
            <MainChatStreamBody
                mainChat={view}
                onPrepare={noop}
                items={items}
                streaming
                streamingStartedAt={0}
                hasProject
                sessionLocalId="main"
                onEditMessage={noop}
                hasMore={false}
                onLoadMore={noop}
            />
            <button type="button" data-testid="fixture-token" onClick={() => setItems((prev) => {
                const last = prev[prev.length - 1];
                return [...prev.slice(0, -1), { ...last, content: `${last.content}\n\nOne more streamed paragraph of tokens.` }];
            })} />
        </>
    );
}

const actions = { onStartChat: vi.fn(), onRunNow: vi.fn(), onOpenMemory: vi.fn(), onOpenAutomations: vi.fn(), onOpenActivity: vi.fn(), onOpenPeople: vi.fn() };
const cron = { jobs: [], loading: false } as unknown as CronJobsProp;
function ActivityHarness() {
    const [section, setSection] = useState<ActivitySectionId>('workedOn');
    return <ActivityPage initialDate="2026-10-22" section={section} onSectionChange={setSection} cronJobs={cron} />;
}

it.skipIf(!process.env.RENDER_SPEED_FIXTURE)('records Profiler commits for chat, today and activity journeys', async () => {
    await i18n.changeLanguage('en');
    resetPageQueries(); resetActivitySessionCache();
    Element.prototype.scrollIntoView = vi.fn();
    globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
    const now = Date.now();
    const { activities, threads } = busyDayFixture();
    fixture.payload = {
        GetTodayNews: { date: '2026-10-22', status: 'ready', items: [], updatedAt: now },
        GetTodayTopics: Array.from({ length: 30 }, (_, i) => ({ id: `topic-${i}`, kind: 'project', name: `Fixture topic ${i}`, status: 'moving' })),
        GetEngagementWeekSummary: { available: false, sent: 0, actedOn: 0, days: 7, kinds: [] },
        GetMobileBriefHealth: { enabled: false }, GetTodaySuggestion: { available: false },
        GetLatestBrief: { brief: null, building: false, enabled: true, nextAt: '', nextSlot: '', empty: false, missing: [] },
        GetCaptureCounts: { paused: false, notReady: false, sources: [] }, GetGoalCheckIn: null,
        GetTopicFinds: { enabled: false, status: 'unconfigured', items: [], topics: [] }, IsClosedEyesModeEnabled: false,
        GetMainChat: { session: { localId: 'main' } },
        GetMainChatOverview: { conversations: 2000, waiting: 0, routine: 0, finished: 0 },
        ListMainChatTopicItems: { sessionId: 'main', items: [], hasMore: false }, ListMainChatAnswerChips: {},
        LoadStreamItemsPage: { items: [], hasMore: false },
        GetActivitiesInRange: activities,
        GetDayThreads: { grouped: true, threads },
        EnsureDailySummary: { autoStatus: 'ready', date: '2026-10-22', summary: { narrative: 'Fixture day summary' } },
        GetKnowledgeEntries: [], GetMemoryEntries: [],
    };

    // 1-3. Chat: cold opens of the 800-row (200-turn, windowed) stream and
    // the original 500-row (125-turn, recent-only) harness, hydration of the
    // find rows, one streamed-token flush, one composer keystroke.
    {
        const commits: Commit[] = [];
        const Profile = makeProfiler(commits);
        // Three cold opens per fixture; the median decides the decision bound
        // (first screen under 200 ms), so machine jitter cannot flake it.
        async function openChat(pairs: number, scenario: string, lastText: string) {
            const opens: { wall: number; commits: Commit[] }[] = [];
            for (let attempt = 0; attempt < 3; attempt++) {
                commits.splice(0);
                const started = performance.now();
                const page = render(<Profile><ChatHarness pairs={pairs} /></Profile>);
                await screen.findAllByText(lastText, undefined, { timeout: 120_000 });
                opens.push({ wall: performance.now() - started, commits: commits.splice(0) });
                page.unmount();
                cleanup();
            }
            opens.sort((a, b) => a.wall - b.wall);
            const median = opens[1];
            report(scenario, median.wall, median.commits);
            const mountMs = median.commits.filter((c) => c.phase === 'mount').reduce((s, c) => s + c.actualDuration, 0);
            const nestedMs = median.commits.filter((c) => c.phase === 'nested-update').reduce((s, c) => s + c.actualDuration, 0);
            expect(mountMs + nestedMs).toBeLessThan(200);
            return median;
        }
        const median = await openChat(200, 'chat.open', 'Pass 199: walk me through commit 199 and what changed in the renderer.');
        expect(median.commits.length).toBeGreaterThan(0);

        // The original 500-row harness, preserved: a long recent-only chat
        // takes the same staged path and holds the same bound.
        await openChat(125, 'chat.openRecentOnly', 'Pass 124: walk me through commit 124 and what changed in the renderer.');

        // The recent section hydrates right after the first paint, so native
        // find reaches all of it — in both fixtures.
        for (const [pairs, last, mid] of [
            [200, 'Pass 199: walk me through commit 199 and what changed in the renderer.', 'Pass 100: walk me through commit 100 and what changed in the renderer.'],
            [125, 'Pass 124: walk me through commit 124 and what changed in the renderer.', 'Pass 60: walk me through commit 60 and what changed in the renderer.'],
        ] as const) {
            const page = render(<Profile><ChatHarness pairs={pairs} /></Profile>);
            await screen.findAllByText(last, undefined, { timeout: 120_000 });
            commits.splice(0);
            const hydrateStarted = performance.now();
            await act(async () => { await new Promise((resolve) => setTimeout(resolve, 400)); });
            await screen.findAllByText(mid, undefined, { timeout: 60_000 });
            report(pairs === 200 ? 'chat.hydrateFindRows' : 'chat.hydrateFindRowsRecentOnly', performance.now() - hydrateStarted, commits.splice(0));
            page.unmount();
            cleanup();
        }

        // Token flush and composer keystroke on the long (800-row) workload,
        // after hydration completes and the deferred highlight queue drains,
        // so the flush measurement is clean.
        render(<Profile><ChatHarness pairs={200} /></Profile>);
        await screen.findAllByText('Pass 199: walk me through commit 199 and what changed in the renderer.', undefined, { timeout: 120_000 });
        await screen.findAllByText('Pass 100: walk me through commit 100 and what changed in the renderer.', undefined, { timeout: 120_000 });
        for (let waited = 0; highlightCacheProbe.queued() !== 0 && waited < 120_000; waited += 200) {
            await act(async () => { await new Promise((resolve) => setTimeout(resolve, 200)); });
        }
        expect(highlightCacheProbe.queued()).toBe(0);
        commits.splice(0);
        const tokenStarted = performance.now();
        await act(async () => { fireEvent.click(screen.getByTestId('fixture-token')); });
        report('chat.streamToken', performance.now() - tokenStarted, commits.splice(0));

        const keyStarted = performance.now();
        await act(async () => { fireEvent.change(screen.getByLabelText('fixture-composer'), { target: { value: 'h' } }); });
        report('chat.composerKeystroke', performance.now() - keyStarted, commits.splice(0));
        cleanup();
    }

    // 4. Switching back to Today (retained in the app; measured here as the
    // content commit a cold revisit would pay).
    {
        const commits: Commit[] = [];
        const Profile = makeProfiler(commits);
        const started = performance.now();
        render(<Profile><TodayPage handlers={actions} /></Profile>);
        await screen.findByText('Fixture topic 0');
        report('today.open', performance.now() - started, commits.splice(0));
        cleanup();
    }

    // 5. Opening Activity with a full day, then a search keystroke (immediate
    // commit plus the 300 ms debounce filter commit).
    {
        const commits: Commit[] = [];
        const Profile = makeProfiler(commits);
        const started = performance.now();
        render(<Profile><ActivityHarness /></Profile>);
        await screen.findAllByText('Fixture thread 0', undefined, { timeout: 60_000 });
        report('activity.open', performance.now() - started, commits.splice(0));

        const keyStarted = performance.now();
        await act(async () => { fireEvent.change(screen.getByPlaceholderText('Search...'), { target: { value: 'thread 1' } }); });
        report('activity.searchKeystroke', performance.now() - keyStarted, commits.splice(0));
        const filterStarted = performance.now();
        await act(async () => { await new Promise((resolve) => setTimeout(resolve, 400)); });
        report('activity.searchFilter', performance.now() - filterStarted, commits.splice(0));
        cleanup();
    }

    resetPageQueries(); resetActivitySessionCache();
}, 300_000);
