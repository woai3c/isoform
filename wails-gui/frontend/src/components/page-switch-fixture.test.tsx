import { readFileSync } from 'node:fs';
import { act, cleanup, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { TodayPage } from './today/TodayPage';
import { ActivityPage, type ActivitySectionId } from './activity-page';
import { MemoryPage } from './memory-page';
import { KnowledgePage } from './knowledge-page';
import { PeoplePage } from './people-page';
import { RetainedPage } from './retained-page';
import { useMainChatPanel } from '@/hooks/useMainChatPanel';
import { useMainChatView } from '@/hooks/useMainChatView';
import { resetPageQueries } from '@/lib/page-queries';
import { resetActivitySessionCache } from './timeline/activity-session-cache';
import type { CronJobsProp } from './timeline/useCalendarData';

// Opt-in unit fixture: every read has 25 ms of simulated bridge/storage delay.
// Measures React content commit + two animation frames in jsdom, not native
// WebView paint. Run against the base sources as well as this branch.
const fixture = vi.hoisted(() => ({ payload: {} as Record<string, unknown>, calls: [] as string[], bindings: new Map<string, (...args: unknown[]) => Promise<unknown>>() }));
vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        if (!fixture.bindings.has(name)) fixture.bindings.set(name, (..._args: unknown[]) => {
            fixture.calls.push(name);
            return new Promise((resolve) => setTimeout(() => resolve(fixture.payload[name] ?? []), 25));
        });
        return fixture.bindings.get(name);
    },
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {}, ClipboardSetText: vi.fn() }));
vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));

const actions = { onStartChat: vi.fn(), onRunNow: vi.fn(), onOpenMemory: vi.fn(), onOpenAutomations: vi.fn(), onOpenActivity: vi.fn(), onOpenPeople: vi.fn() };
const cron = { jobs: [], loading: false } as unknown as CronJobsProp;
function Chat() {
    const panel = useMainChatPanel(true);
    const view = useMainChatView({ enabled: true, items: panel.items });
    return <output>{panel.status === 'ready' && view.overviewStatus === 'ready' ? panel.items[0]?.content : ''}</output>;
}
function Activity() {
    const [section, setSection] = useState<ActivitySectionId>('workedOn');
    return <ActivityPage initialDate="2026-10-22" section={section} onSectionChange={setSection} cronJobs={cron} />;
}

it.skipIf(!process.env.PAGE_SWITCH_FIXTURE)('records cold and repeat visits for the six pages', async () => {
    await i18n.changeLanguage('en');
    resetPageQueries(); resetActivitySessionCache();
    Element.prototype.scrollIntoView = vi.fn();
    globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
    const now = Date.now();
    const belief = { id: 'belief', title: 'Fixture belief', content: '# Fixture belief\n\nA public preference.', dimension: 'user_insight', source: 'activity', modTime: now, updatedAt: now, sizeBytes: 20, apps: [], activitySessionIds: [] };
    const knowledge = { id: 'knowledge', title: 'Fixture know-how', kind: 'reference', takeaway: 'A public reference.', points: [], apps: [], sessionCount: 0, savedAt: now, reuseCount: 0, topicId: '', topicName: '' };
    fixture.payload = {
        GetTodayNews: { date: '2026-10-22', status: 'ready', items: [], updatedAt: now },
        GetTodayTopics: Array.from({ length: 30 }, (_, i) => ({ id: `topic-${i}`, kind: 'project', name: `Fixture topic ${i}`, status: 'moving' })),
        GetEngagementWeekSummary: { available: false, sent: 0, actedOn: 0, days: 7, kinds: [] },
        GetMobileBriefHealth: { enabled: false }, GetTodaySuggestion: { available: false },
        GetLatestBrief: { brief: null, building: false, enabled: true, nextAt: '', nextSlot: '', empty: false, missing: [] },
        GetCaptureCounts: { paused: false, notReady: false, sources: [] }, GetGoalCheckIn: null,
        GetTopicFinds: { enabled: false, items: [] }, IsClosedEyesModeEnabled: false,
        GetMainChat: { session: { localId: 'main' } },
        LoadStreamItemsPage: { items: [{ id: 'answer', kind: 'assistant', content: 'Fixture chat answer', createdAt: now }], hasMore: true, nextCursor: 1950 },
        GetMainChatOverview: { conversations: 2000, waiting: 0, routine: 0, finished: 0 },
        ListMainChatTopicItems: { sessionId: 'main', items: [], hasMore: false }, ListMainChatAnswerChips: {},
        GetActivitiesInRange: [{ id: 'activity', title: 'Fixture activity', description: 'Public fixture work', latestChange: '', apps: [], thumbnails: [], entities: [], runCount: 0, activeMs: 600000, inProgress: false, status: 'settled', category: 'work', runIds: [], startMs: now - 600000, endMs: now, activeMsByApp: {} }],
        GetDayThreads: { grouped: true, threads: [{ id: 'thread', title: 'Fixture activity', kind: 'work', activityIds: ['activity'], startMs: now - 600000, endMs: now, activeMs: 600000, timeSpentMs: 600000, stretches: [{ start: now - 600000, end: now, activeMs: 600000, activityIds: ['activity'], resumePoint: null }], gaps: [], linkReason: { kind: 'rule', keys: [], text: '' }, unfinished: false, interruption: false }] }, EnsureDailySummary: { autoStatus: 'ready', date: '2026-10-22', summary: { narrative: 'Fixture day summary' } },
        GetMemoryEntries: [belief], GetAboutYou: { beliefs: [{ id: 'belief', title: belief.title, status: 'inferred', confidence: 2 }], newThisWeek: [], totals: { beliefs: 1 }, storage: { files: 1, bytes: 20, chunks: 1 } },
        GetAboutYouPortrait: { state: 'learning', generatedAt: 0, beliefCount: 0, writing: false, rewriting: false, forgotten: false, assistant: '' }, GetMemoryStatus: {},
        ListKnowHow: [knowledge], GetKnowledgeEntries: [{ ...knowledge, content: '# Fixture know-how\n\nA public reference.', date: '2026-10-22', sessionIds: [] }], GetKnowHowMatch: { available: false },
        ListPeopleOverview: { open: [], everyone: [{ id: 'person', kind: 'human', name: 'Fixture person', loops: [] }], duplicates: [], headline: {} }, ListPrincipalsWithStats: [], GetPersonDetail: { id: 'person', name: 'Fixture person', relationship: '', notes: '', researchSummary: '', loops: [], together: [], sharedTopics: [], seenIn: [], messageCount: 0 }, GetPersonResearch: null, GetVoiceEnrollmentStatus: {}, GetContactsAccessStatus: { status: 'not_requested' },
    };
    const retained = readFileSync('src/App.tsx', 'utf8').includes('<RetainedPage');
    const specs = [
        { name: 'Today', text: 'Fixture topic 0', node: <TodayPage handlers={actions} />, retain: retained },
        { name: 'Main chat', text: 'Fixture chat answer', node: <Chat />, retain: retained },
        { name: 'Activity', text: 'Fixture activity', node: <Activity />, retain: false },
        // The three dialog pages are retained after their first open (their
        // App dialogs keep them mounted), so the revisit is a show, not a
        // remount.
        { name: 'About you', text: 'Fixture belief', node: <MemoryPage />, retain: retained },
        { name: 'Know-how', text: 'Fixture know-how', node: <KnowledgePage />, retain: retained },
        { name: 'People', text: 'Fixture person', node: <PeoplePage />, retain: retained },
    ];
    for (const spec of specs) {
        fixture.calls = [];
        const started = performance.now();
        const page = render(spec.retain ? <RetainedPage active page={spec.name}>{spec.node}</RetainedPage> : spec.node);
        await screen.findAllByText(spec.text);
        const coldContentMs = performance.now() - started;
        await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
        const coldPaintMs = performance.now() - started;
        await act(async () => { await new Promise((resolve) => setTimeout(resolve, 100)); });
        const coldBindings = [...fixture.calls];
        if (spec.retain) page.rerender(<RetainedPage active={false} page={spec.name}>{spec.node}</RetainedPage>);
        else page.rerender(<></>);
        fixture.calls = [];
        const revisit = performance.now();
        page.rerender(spec.retain ? <RetainedPage active page={spec.name}>{spec.node}</RetainedPage> : spec.node);
        await screen.findAllByText(spec.text);
        const revisitContentMs = performance.now() - revisit;
        await new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
        const revisitPaintMs = performance.now() - revisit;
        await act(async () => { await new Promise((resolve) => setTimeout(resolve, 100)); });
        console.info('[page-fixture]', JSON.stringify({ page: spec.name, optimized: retained, retained: spec.retain, coldContentMs, revisitContentMs, coldPaintMs, revisitPaintMs, coldBindings, revisitBindings: fixture.calls }));
        expect(coldBindings.length).toBeGreaterThan(0);
        cleanup();
    }
    resetPageQueries(); resetActivitySessionCache();
}, 30_000);
