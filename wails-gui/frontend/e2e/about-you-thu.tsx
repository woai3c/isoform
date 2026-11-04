// Fixture: the real About you page with synthetic host replies (fictional people).
// ?state= empty|dayone|page|v101|v102|couldntUpdate|couldntUse|learning|rewriting, ?fail=Binding,Binding, ?select=, ?lang=, ?theme=paper|onedark
import { createRoot } from 'react-dom/client';
import '@/index.css';

const params = new URLSearchParams(location.search);
const state = params.get('state') ?? 'page';
const fail = new Set((params.get('fail') ?? '').split(',').filter(Boolean));
const now = Date.now();
const day = 86_400_000;
const noop = () => {};
const listeners = new Map<string, Set<(value: unknown) => void>>();
const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

function belief(id: string, title: string, body: string, dimension = 'preference') {
    return {
        id, title, dimension, source: 'activity',
        content: `# ${title}\n- **Dimension**: ${dimension}\n- **Source**: activity\n\n${body}\n`,
        modTime: now - 3 * day, updatedAt: now - 3 * day, sizeBytes: 160, hasHistory: false, activitySessionIds: [], apps: [],
    };
}
const beliefs = state === 'empty' ? [] : [
    belief('mornings', 'Guards mornings for deep work', 'Keeps 8 to 11 AM free for writing and code review, and declines meetings then.'),
    belief('replies', 'Writes short replies', 'Replies to Maya Okafor and the design team in two sentences or fewer.'),
    belief('calendar', 'Puts every plan on the calendar', 'Blocks time for each plan, from release checklists to trail runs with Theo Lindqvist.'),
];
const about = {
    beliefs: beliefs.map((row) => ({
        id: row.id, title: row.title, status: (state === 'v101' || state === 'v102') && row.id === 'replies' ? 'learned_today' : 'inferred', confidence: 3, firstLearnedAt: state === 'v101' && row.id === 'replies' ? now - 3_600_000 : now - 12 * day, updatedAt: row.updatedAt,
        sessionCount: state === 'v101' && row.id === 'replies' ? 0 : 9, dayCount: 6, confirmedAt: 0, source: state === 'dayone' ? 'assistant-history' : 'activity', notQuiteAt: 0, portraitCandidate: false, area: 'work',
        evidenceFirstAt: now - 12 * day, evidenceLastAt: now - day,
    })),
    newThisWeek: state === 'dayone' ? ['mornings', 'replies'] : state === 'v101' ? ['replies'] : [],
    totals: { beliefs: beliefs.length, confirmed: 0 },
    storage: { files: beliefs.length, bytes: 480, chunks: 6, vectorReady: true },
};
const part = (text: string, memoryIds: string[] = [], confirmed = false) => ({ text, memoryIds, confirmed });
const ready = {
    state: 'ready', name: ['blueprint', 'builder'], nameAreas: [['work', 'routines'], ['tools']], sides: [0.2, 0.3, 0.2, 0.3],
    sentences: [
        { role: 'connect', changed: false, parts: [part('You guard your time before noon', ['mornings']), part(', and '), part('you put every plan on the calendar', ['calendar']), part('.')] },
        { role: 'action', changed: false, parts: [part('So Isoform '), part('keeps your replies short', ['replies']), part('.')] },
    ],
    lately: null,
    workWithMe: [
        { text: 'Book meetings after 11 AM; mornings are for deep work.', memoryIds: ['mornings'], area: 'routines', confirmed: false },
        { text: 'Put the answer in the first line; I reply in about two sentences.', memoryIds: ['replies'], area: 'communication', source: 'slack', confirmed: false },
    ],
    beliefCount: beliefs.length, generatedAt: now - 2 * 3_600_000, writing: false, rewriting: false, forgotten: false,
    assistant: 'claude-code', candidates: [],
};
const learning = {
    state: 'learning', name: null, sentences: [], workWithMe: [], lately: null, candidates: [], generatedAt: 0, beliefCount: beliefs.length,
    writing: false, rewriting: false, forgotten: false, assistant: '',
    progress: { beliefs: 9, beliefsNeeded: 20, days: 2, daysNeeded: 5, groups: 2, groupsNeeded: 3, moreDays: 3, met: false, sidesReady: false },
};
const portrait = state === 'empty' || state === 'dayone' || state === 'learning' ? learning
    : state === 'couldntUse' ? { ...ready, failure: { reason: 'unusable', at: now, retryAt: now + 3 * 3_600_000, assistant: 'claude-code' } }
    : state === 'couldntUpdate' ? { ...ready, failure: { reason: 'limit', at: now, retryAt: now + 3 * 3_600_000, assistant: 'claude-code' } }
    : state === 'rewriting' ? { ...ready, rewriting: true, saidAt: now - 60_000 }
    : ready;
const reads: Record<string, unknown> = {
    GetMemoryEntries: beliefs,
    GetMemoryStatus: { files: beliefs.length, chunks: 6, vectorReady: true },
    GetAboutYou: about,
    GetAboutYouPortrait: portrait,
    // Go's mobileGoalTitle gives the dictated commitment its short name (Y30).
    ListGoals: state === 'v101' ? { tracking: [{
        id: 'afo', eventId: 'afo-1', kind: 'commitment', title: 'AFO calf raises, every day', state: 'active', group: 'tracking',
        slipping: true, dueAt: '2026-10-31T23:59:59-07:00', dueDate: '2026-10-31', addedAt: '2026-10-20T09:00:00-07:00',
        updatedAt: '2026-10-31T09:00:00-07:00', lastEvent: 'created', origin: 'chat',
    }], done: [], held: [] } : { tracking: [], done: [], held: [] },
    GetEntryUses: { total: 0, lastAt: 0, surfaces: [] },
    GetCommunicationDraftExamples: [], // v1.0.2 Y35: one belief has a source to show, the others have none.
    GetMemoryEvidenceReadable: (id: string) => id === 'mornings'
        ? [{ at: now - day, durationMs: 0, appName: 'calendar', text: 'You declined 6 meetings before 11 AM in the last two weeks.', activitySessionId: '' }]
        : [], ListMemoryVersions: [], SearchMemory: [],
    GetMemoryForgetPreview: {
        memoryId: 'mornings', title: 'Guards mornings for deep work',
        items: [{ kind: 'memory', id: 'mornings', title: 'Guards mornings for deep work' }, { kind: 'self_dream', id: 'self-1', title: 'Morning focus pattern' }],
        embeddingCount: 3, historyCount: 1, retentionDays: 30, confirmationToken: 'fixture',
    },
    GetFirstWeekDay: state === 'dayone' ? 0 : 6,
    GetAssistantHistory: { enabled: true, state: 'ready', assistants: ['claude-code'], projects: [], beliefCount: 3, knowHowCount: 0, scannedAt: now - 3_600_000, pass2Done: true },
    GetCalendarFirstDay: null,
    ScanAIMemories: [],
    CreateGoal: (input: { text: string }) => ({ id: 'goal-ridge', eventId: 'goal-ridge-1', state: 'tracking', title: input.text }),
    ConfirmAboutYouPortrait: ['mornings', 'calendar', 'replies'],
    LoadUIState: '',
};
(window as any).go = { main: { App: new Proxy({}, { get: (_, method: string) => async (...args: unknown[]) => {
    await sleep(30);
    if (fail.has(method)) throw new Error(`${method} failed`);
    const value = reads[method];
    if (typeof value === 'function') return value(...args);
    return value === undefined ? null : value;
} }) } };
(window as any).runtime = new Proxy({}, { get: (_, name: string) => {
    if (name === 'EventsOn' || name === 'EventsOnMultiple') return (event: string, callback: (value: unknown) => void) => {
        const set = listeners.get(event) ?? new Set();
        set.add(callback);
        listeners.set(event, set);
        return () => { set.delete(callback); };
    };
    if (name === 'ClipboardSetText') return async () => { if (fail.has('ClipboardSetText')) throw new Error('clipboard'); return true; };
    return () => noop;
} });
(window as any).emit = (event: string, payload: unknown) => { for (const callback of listeners.get(event) ?? []) callback(payload); };

if (params.get('theme')) localStorage.setItem('yansu.theme', params.get('theme')!);
else localStorage.removeItem('yansu.theme');
const [{ default: i18n }, { ThemeProvider }, { MemoryPage }] = await Promise.all([
    import('@/i18n'), import('@/hooks/useTheme'), import('@/components/memory-page'),
]);
await i18n.changeLanguage(params.get('lang') ?? 'en');
createRoot(document.getElementById('root')!).render(
    <ThemeProvider>
        <div className="flex h-screen w-screen overflow-hidden">
            <aside aria-hidden style={{ width: 232, flexShrink: 0 }} className="border-r border-border bg-sidebar" />
            <main className="patina-ground flex min-w-0 flex-1 flex-col bg-background">
                <MemoryPage active initialSelectedId={params.get('select') ?? undefined} />
            </main>
        </div>
    </ThemeProvider>,
);
