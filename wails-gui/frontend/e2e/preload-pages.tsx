// Browser fixture for the preload harness: real page components over synthetic
// reads with a fixed per-binding latency, so a WebKit pass can time navigation
// -> first real data for every sidebar page and Settings, cold and warm.
// About you / How you do things / Who you know mount in the same retained
// dialog wrapper App uses; Search opens its real overlay. URL params:
// lat=<ms per binding> (default 120), prefetch=1 (run the idle prefetch walk
// after Today's first paint before any navigation).
import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import i18n from '@/i18n';
import '@/index.css';
import { beginPageSwitch, markPageContent } from '@/lib/page-switch-timing';
import { TodayPage } from '@/components/today/TodayPage';
import { ActivityPage, type ActivitySectionId } from '@/components/activity-page';
import { GoalsPage } from '@/components/goals/GoalsPage';
import { MemoryPage } from '@/components/memory-page';
import { KnowledgePage } from '@/components/knowledge-page';
import { PeoplePage } from '@/components/people-page';
import { SettingsPage } from '@/components/settings-page';
import { SearchDialog, type SearchDialogActions } from '@/components/search-dialog';
import { Dialog, DialogTitle } from '@/components/ui/dialog';
import { RetainedDialogContent } from '@/components/retained-dialog-content';
import { useMainChatPanel } from '@/hooks/useMainChatPanel';
import { useMainChatView } from '@/hooks/useMainChatView';
import type { CronJobsProp } from '@/components/timeline/useCalendarData';
import type { TodayActionHandlers } from '@/components/today/types';

const params = new URLSearchParams(location.search);
const LATENCY = Number(params.get('lat') ?? 120);
const PREFETCH = params.get('prefetch') === '1';
const noop = () => {};

const now = Date.now();
const day = new Date();
const dateStr = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, '0')}-${String(day.getDate()).padStart(2, '0')}`;
const beliefs = Array.from({ length: 30 }, (_, i) => ({
    id: `belief-${i}`, title: `Fixture belief ${i}`, content: `# Fixture belief ${i}\n\nA public preference.`,
    dimension: 'user_insight', source: 'activity', modTime: now, updatedAt: now, sizeBytes: 20, apps: [], activitySessionIds: [],
}));
const knowledge = { id: 'knowledge', title: 'Fixture know-how', kind: 'reference', takeaway: 'A public reference.', points: [], apps: [], sessionCount: 0, savedAt: now, reuseCount: 0, topicId: '', topicName: '' };
const brief = {
    version: 1, briefId: 'layout-morning', slot: 'morning', localDate: dateStr, asOf: new Date().toISOString(), timezone: 'America/Los_Angeles', coldStart: false,
    narrativeStatus: 'ok', narrative: [{ text: 'The task list is ready for review.', refs: ['e1'], topicId: 'cleanup', topicName: 'Fixture topic' }],
    cards: [], sections: { moved: [], doing: [], watching: [], meetings: [], people: [], openLoops: [], learned: [], deliverables: [], schedules: [] }, evidence: [],
};
const reads: Record<string, unknown> = {
    GetLatestBrief: { brief, building: false, enabled: true, nextAt: '', nextSlot: '', empty: false, missing: [] },
    GetTodayNews: { date: dateStr, status: 'ready', items: [], updatedAt: now },
    GetTodayNewsHistory: [],
    GetTodaySuggestion: { available: false }, GetGoalCheckIn: null, GetTopicFinds: { status: 'off', items: [], topics: [] },
    GetTodayTopics: [
        { id: 'topic-0', kind: 'project', name: 'Fixture topic 0', status: 'moving', latest: { text: 'Updated', at: new Date().toISOString() }, pinnedToSidebar: false },
        { id: 'topic-1', kind: 'project', name: 'Fixture topic 1', status: 'moving', latest: { text: 'Updated', at: new Date().toISOString() }, pinnedToSidebar: false },
    ],
    GetTodayQuietLines: [], IsClosedEyesModeEnabled: false,
    GetCaptureCounts: { paused: false, notReady: false, sources: [] },
    GetEngagementWeekSummary: { available: false, sent: 0, actedOn: 0, days: 7, kinds: [] },
    GetMobileBriefHealth: { enabled: false },
    GetMainChat: { session: { localId: 'main' } },
    LoadStreamItemsPage: { items: [{ id: 'answer', kind: 'assistant', content: 'Fixture chat answer', createdAt: now }], hasMore: false, nextCursor: 0 },
    GetMainChatOverview: { conversations: 1, waiting: 0, finished: 0, routine: 0 },
    ListMainChatWaitingItems: { items: [], hasMore: false, nextCursor: '' },
    ListMainChatAnswerChips: {},
    GetActivitiesInRange: [{ id: 'activity', title: 'Fixture activity', description: 'Public fixture work', latestChange: '', apps: [], thumbnails: [], entities: [], runCount: 0, activeMs: 600000, inProgress: false, status: 'settled', category: 'work', runIds: [], startMs: now - 600000, endMs: now, activeMsByApp: {} }],
    GetDayThreads: { grouped: true, threads: [{ id: 'thread', title: 'Fixture activity', kind: 'work', activityIds: ['activity'], startMs: now - 600000, endMs: now, activeMs: 600000, timeSpentMs: 600000, stretches: [{ start: now - 600000, end: now, activeMs: 600000, activityIds: ['activity'], resumePoint: null }], gaps: [], linkReason: { kind: 'rule', keys: [], text: '' }, unfinished: false, interruption: false }] },
    EnsureDailySummary: { autoStatus: 'ready', date: dateStr, summary: { narrative: 'Fixture day summary' } },
    GetCronRunsByDateRange: [], GetKnowledgeEntries: [{ ...knowledge, content: '# Fixture know-how\n\nA public reference.', date: dateStr, sessionIds: [] }],
    GetMemoryEntries: beliefs, GetRecentMeetingNotes: [], GetDayNoticed: { items: [] },
    GetHostBridgePort: 18080, GetHostBridgeMediaToken: 'fixture-token', GetAppIconsForBundleIDs: {},
    ListGoals: { tracking: [], done: [], held: [] },
    GetAboutYou: { beliefs: beliefs.map((b) => ({ id: b.id, title: b.title, status: 'inferred', confidence: 2 })), newThisWeek: [], totals: { beliefs: beliefs.length, confirmed: 0 }, storage: { files: 1, bytes: 20, chunks: 1, vectorReady: false } },
    GetAboutYouPortrait: { state: 'none', segments: [] }, GetMemoryStatus: {},
    ListKnowHow: [knowledge], GetKnowHowMatch: null,
    ListPeopleOverview: { open: [], everyone: [{ id: 'person', kind: 'human', name: 'Fixture person', loops: [] }], duplicates: [], headline: { repliesOwed: 0, thingsOwed: 0, peopleWaitingOnYou: 0 } },
    // A self principal makes Who you know open its detail pane (the profile
    // path) with the voice fold's reads — the dialog's heaviest open.
    ListPrincipalsWithStats: [{ principal: { id: 'self', kind: 'self', displayName: 'Fixture You', relationship: '', notes: '', createdFrom: 'test', confidence: 1, aliases: [], createdAt: now, updatedAt: now }, stats: { principalId: 'self', totalMessages: 0, firstSeenAt: now, lastSeenAt: now, byApp: [], recentMessages: [] } }],
    GetContactsAccessStatus: { status: 'not_requested' },
    GetPersonDetail: { id: 'self', name: 'Fixture You', relationship: '', notes: '', researchSummary: '', loops: [], together: [], sharedTopics: [], seenIn: [], messageCount: 0 },
    GetVoiceEnrollmentStatus: { principalId: 'self', sampleCount: 0, lastCapturedAt: 0, modelReady: false, modelMissingHint: '' },
    ListVoiceSamples: [],
    ListPrincipalActivitySessions: [],
    GetSettingsOverview: {
        unavailable: [],
        sees: { watching: true, pausedUntil: null, ignoredAppsCount: 0, passwordManagersHidden: false },
        speaksUp: { level: 'normal', dailyCap: 12, quietStart: '', quietEnd: '', channels: ['desktop'] },
        thinksWith: { order: [{ assistant: 'claude', percentLeft: 82, windowLabel: '5 hours', resetsAt: '' }] },
        remembers: { awaitingConfirmCount: 0 },
        keeps: { retention: 30, limitBytes: 0, usedBytes: 123456 },
        briefs: { morningAt: '08:00', eveningAt: '17:00', adaptive: false, phone: null },
        ignores: { apps: [] },
        worthALook: [],
    },
    GetMobileBriefSchedule: { enabled: false, morning: '08:00', evening: '17:00', lastGeneratedAt: '', lastSlot: '', narrativeAvailable: false, feedEnabled: false, feedUrl: '' },
    GetPlatformPermissions: { platform: 'darwin', items: [], supported: true },
    GetBackgroundListening: false, GetAutomaticMeetingRecordingEnabled: false,
    GetTextChannelStatus: { supported: false, enabled: false, number: '', includeNames: false, consentAccepted: false, permissionGranted: false, sentToday: 0, lastSentAt: '', lastError: '', verified: false, verifying: false },
    GetSMSChannelStatus: { available: false, state: '', number: '', pendingNumber: '', codeExpiresAt: '', sentToday: 0, sentThisMonth: 0, dailyCap: 0, monthlyCap: 0, lastError: '' },
    LoadUIState: '',
    ListHandoffs: [],
};
const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
// Every binding waits LATENCY ms, like a real bridge read; unknown reads
// return null rather than throwing so an untouched control cannot crash a
// page. The in-flight count lets the harness time "complete" (all of a
// page's reads settled), not just first paint.
let readsPending = 0;
(window as any).__readsPending = 0;
(window as any).go = { main: { App: new Proxy({}, { get: (_, method: string) => async () => {
    readsPending += 1;
    (window as any).__readsPending = readsPending;
    try {
        await sleep(LATENCY);
        const value = reads[method];
        return value === undefined ? null : value;
    } finally {
        readsPending -= 1;
        (window as any).__readsPending = readsPending;
    }
} }) } };
(window as any).runtime = new Proxy({}, { get: () => () => noop });

const handlers: TodayActionHandlers = {
    onStartChat: noop, onRunNow: noop, onOpenMemory: noop, onOpenAutomations: noop, onOpenActivity: noop, onOpenPeople: noop, onOpenSettings: noop, onOpenTopic: noop,
};
const cron = { jobs: [], loading: false } as unknown as CronJobsProp;
const searchActions = new Proxy({} as SearchDialogActions, { get: () => noop });

function Chat() {
    const panel = useMainChatPanel(true);
    const view = useMainChatView({ enabled: true, items: panel.items });
    const ready = panel.status === 'ready' && view.overviewStatus === 'ready';
    useEffect(() => { if (ready) markPageContent('chat'); }, [ready]);
    return <output data-testid="preload-chat" style={{ display: 'block', padding: 24 }}>{ready ? panel.items[0]?.content ?? '' : ''}</output>;
}

function Activity() {
    const [section, setSection] = useState<ActivitySectionId>('workedOn');
    return <ActivityPage section={section} onSectionChange={setSection} cronJobs={cron} />;
}

const PAGES = ['today', 'chat', 'activity', 'goals', 'about-you', 'know-how', 'people', 'settings', 'search'] as const;
type PageId = (typeof PAGES)[number];
/** Pages that open as overlays over the current page (dialogs in the app). */
const OVERLAYS: ReadonlySet<string> = new Set(['about-you', 'know-how', 'people', 'search']);

/** The same wrapper App uses for these pages: a Radix dialog that stays
 * mounted, its content retained after the first open. */
function PageDialog({ open, onClose, title, pageId, children }: { open: boolean; onClose: () => void; title: string; pageId: string; children: React.ReactNode }) {
    return (
        <Dialog open={open} onOpenChange={(next) => { if (!next) onClose(); }}>
            <RetainedDialogContent open={open} retain={false} page={pageId} className="p-0 w-[90vw] max-w-none h-[88vh] max-h-none overflow-hidden">
                <DialogTitle className="sr-only">{title}</DialogTitle>
                {children}
            </RetainedDialogContent>
        </Dialog>
    );
}

function FixtureApp() {
    const [page, setPage] = useState<PageId>('today');
    // Overlays (about-you / know-how / people dialogs, search) open over the
    // current page, exactly like the app.
    const [overlay, setOverlay] = useState<PageId | null>(null);
    const nav = (next: PageId) => {
        if (OVERLAYS.has(next)) {
            if (overlay === next) return;
            beginPageSwitch(next);
            setOverlay(next);
            return;
        }
        if (next === page) return;
        beginPageSwitch(next);
        setPage(next);
    };
    return (
        <div className="flex h-screen w-screen">
            <nav className="flex w-40 shrink-0 flex-col gap-1 border-r border-border-subtle p-3">
                {PAGES.map((id) => (
                    <button key={id} type="button" data-nav={id} onClick={() => nav(id)}
                        className={`rounded-lg px-2 h-8 text-left text-[13px] ${(OVERLAYS.has(id) ? overlay === id : page === id) ? 'bg-surface-active text-ink' : 'text-muted hover:bg-surface-hover'}`}>
                        {id}
                    </button>
                ))}
            </nav>
            <main data-testid="preload-area" className="flex min-w-0 flex-1 flex-col">
                {page === 'today' && (
                    <TodayPage
                        handlers={handlers}
                        onContentReady={() => {
                            markPageContent('today');
                            // Dynamic so this fixture also runs against a base
                            // tree that predates the idle prefetch module.
                            if (PREFETCH) void import('@/lib/idle-prefetch').then((mod) => mod.startIdlePrefetch(), () => undefined).then(() => { (window as any).__idlePrefetchDone = true; });
                        }}
                    />
                )}
                {page === 'chat' && <Chat />}
                {page === 'activity' && <Activity />}
                {page === 'goals' && <GoalsPage />}
                {page === 'settings' && (
                    <SettingsPage
                        onClose={noop}
                        onReplayOnboarding={noop}
                        vimMode={false}
                        onVimModeChange={noop}
                        keybindings={{} as never}
                        onOpenMemory={noop}
                        onOpenPeople={noop}
                        onOpenProfile={noop}
                    />
                )}
            </main>
            {/* Retained dialogs stay mounted across closes (visited latch),
                exactly like App: never rendered until their first open. */}
            <PageDialog open={overlay === 'about-you'} onClose={() => setOverlay(null)} title="About you" pageId="about-you"><MemoryPage /></PageDialog>
            <PageDialog open={overlay === 'know-how'} onClose={() => setOverlay(null)} title="How you do things" pageId="know-how"><KnowledgePage /></PageDialog>
            <PageDialog open={overlay === 'people'} onClose={() => setOverlay(null)} title="Who you know" pageId="people"><PeoplePage initialSelection="self" /></PageDialog>
            <SearchDialog open={overlay === 'search'} onOpenChange={() => setOverlay(null)} projects={[]} actions={searchActions} />
        </div>
    );
}

await i18n.changeLanguage('en');
// Before the first render, like App's layout-effect begin on the boot page.
beginPageSwitch('today');
createRoot(document.getElementById('root')!).render(<FixtureApp />);
