// Isolated component fixture: the real portrait card, Today card and CSS with
// synthetic host replies. Never start the app, its bridge, a model or a
// user-data store. ?state= picks a portrait v1.0.0 §8 or v2.0.0 state,
// ?theme=dark the dark theme, ?language= the language.
import { createRoot } from 'react-dom/client';
import '@/index.css';
import i18n from '@/i18n';
import { AboutYouPortraitCard, type PortraitEvidence } from '@/components/about-you/portrait';
import { TodayPortraitCard } from '@/components/today/TodayPortraitCard';
import { NewThisWeek, useBeliefReview, type ReviewItem } from '@/components/about-you/new-this-week';
import type { AboutYouPortrait } from '@/lib/wails-shim-app';

const params = new URLSearchParams(location.search);
const state = params.get('state') ?? 'ready';
if (params.get('theme') === 'dark') document.documentElement.setAttribute('data-theme', 'onedark');
const noop = () => {};
const now = Date.now();
const listeners = new Map<string, Set<(value: unknown) => void>>();
const fixture = window as unknown as Record<string, any>;

const part = (text: string, memoryIds?: string[], confirmed = false) => ({ text, memoryIds, confirmed });
// The sentences the real model wrote from the 60-belief fixture
// (TestPortraitWithRealModel), so the screenshots show real output.
function portrait(overrides: Partial<AboutYouPortrait> = {}): AboutYouPortrait {
    return {
        state: 'ready',
        name: ['blueprint', 'builder'],
        nameAreas: [['work', 'routines'], ['tools']],
        sides: [0.17, 0.14, 0.25, 0.14],
        sentences: [
            { role: 'connect', changed: false, parts: [part('You guard your time before noon', ['deep']), part(', and '), part('you put every plan on the calendar', ['calendar']), part('.')] },
            { role: 'insight', changed: false, parts: [part('You start work at 8 AM', ['start'], true), part(', but '), part('you move meetings to after 2 PM', ['meetings']), part('.')] },
            { role: 'life', changed: false, parts: [part('Outside work, you plan ahead too, from hikes a week out to trips booked months out', ['hikes', 'trips']), part('.')] },
            { role: 'action', changed: false, parts: [part('So Isoform '), part('keeps your mornings clear in your brief', ['deep']), part(', and '), part('lets your coding assistants check when you focus', ['focus']), part('.')] },
        ],
        lately: {
            parts: [part('One deep morning a week this month. You usually have three: '), part('meetings have been moving before noon', ['deep']), part('.')],
            memoryId: 'deep', thisMonth: 1, usual: 3,
        },
        workWithMe: [
            { text: 'Book meetings after 2 PM; mornings are for deep work.', memoryIds: ['meetings'], area: 'routines', confirmed: false },
            { text: 'Send the plan as a written doc before we meet.', memoryIds: ['written'], area: 'work', confirmed: false },
            { text: 'Put the answer in the first line; I reply in about two sentences.', memoryIds: ['first'], area: 'communication', confirmed: true },
            { text: "Don't book meetings before 11 AM; I decline them then.", memoryIds: ['decline'], area: 'routines', confirmed: false },
        ],
        beliefCount: 60,
        generatedAt: now - 2 * 3_600_000,
        writing: false, rewriting: false, forgotten: false,
        assistant: 'claude-code',
        candidates: [], confirming: false,
        ...overrides,
    };
}
const empty = { name: null, sentences: [], workWithMe: [], lately: null } as Partial<AboutYouPortrait>;
const rows = [
    { memoryId: 'no', text: 'Sends replies that say no about two days after they arrive', answer: '' as const },
    { memoryId: 'yes', text: 'Says yes within about 20 minutes', answer: '' as const },
    { memoryId: 'move', text: 'Moves morning meetings to the afternoon', answer: '' as const },
];
const progress = { beliefs: 9, beliefsNeeded: 20, days: 2, daysNeeded: 3, groups: 2, groupsNeeded: 3, moreDays: 2, met: false };
const states: Record<string, () => AboutYouPortrait> = {
    ready: () => portrait(),
    learning: () => portrait({ ...empty, state: 'learning', progress }),
    writing: () => portrait({ ...empty, state: 'writing', writing: true }),
    failed: () => portrait({ ...empty, state: 'failed', failure: { reason: 'no_answer', at: now, retryAt: now + 15 * 60_000, assistant: 'claude-code' } }),
    rewriting: () => portrait({ rewriting: true }),
    rewritten: () => {
        const next = portrait({ generatedAt: now });
        next.sentences![1] = { role: 'insight', changed: true, parts: [part('You start at 8 AM', ['start'], true), part(', and '), part('keep the afternoon for people', ['meetings']), part('.')] };
        return next;
    },
    forgotten: () => {
        const next = portrait({ forgotten: true });
        next.sentences = next.sentences!.filter((sentence) => sentence.role !== 'insight');
        return next;
    },
    couldntUpdate: () => portrait({ failure: { reason: 'limit', at: now, retryAt: now + 3 * 3_600_000, assistant: 'claude-code' } }),
    // v2.0.0: what survived, with the confirm rows; only the shape; the AI at
    // its limit with no portrait yet.
    survived: () => {
        const next = portrait({ candidates: rows });
        next.sentences = next.sentences!.filter((sentence) => sentence.role === 'connect' || sentence.role === 'life');
        next.lately = null;
        return next;
    },
    confirmed: () => {
        const next = portrait({ generatedAt: now });
        next.sentences![1] = { ...next.sentences![1], changed: true };
        return next;
    },
    shape: () => portrait({ state: 'shape', sentences: [], lately: null, candidates: rows, workWithMe: portrait().workWithMe!.slice(0, 3) }),
    limit: () => portrait({ ...empty, state: 'failed', failure: { reason: 'limit', at: now, retryAt: new Date(new Date().setHours(16, 0, 0, 0)).getTime(), assistant: 'claude-code' } }),
    // Answer feedback v1.0.0: kept on a later open with a newer version
    // waiting, a rewrite from the note, a sentence back from a confirm row.
    kept: () => portrait({ saidAt: now - 86_400_000, pending: true }),
    rewrittenNote: () => {
        const next = portrait({ generatedAt: now });
        next.sentences![1] = { role: 'insight', changed: true, cause: { note: 'I start early, but afternoons are for people.', notQuite: true }, parts: [part('You start at 8 AM', ['start'], true), part(', and '), part('keep the afternoon for people', ['meetings']), part('.')] };
        return next;
    },
    back: () => {
        const next = portrait({ generatedAt: now, candidates: [] });
        next.sentences![1] = { ...next.sentences![1], changed: true, cause: { confirm: ['meetings'], note: '', notQuite: false }, parts: [part('You start work at 8 AM', ['start'], true), part(', but '), part('you move meetings to after 2 PM', ['meetings'], true), part('.')] };
        return next;
    },
};
let current = (states[state] ?? states.ready)();
fixture.runtime = {
    EventsOn: (name: string, callback: (value: unknown) => void) => {
        const set = listeners.get(name) ?? new Set();
        set.add(callback);
        listeners.set(name, set);
        return () => { set.delete(callback); };
    },
};
// A newer version arrives while the card is open: "A new version is ready".
fixture.newVersion = () => {
    current = portrait({ generatedAt: now, name: ['curious', 'connector'] });
    for (const callback of listeners.get('portrait:updated') ?? []) callback(undefined);
};
fixture.go = { main: { App: new Proxy({}, { get: (_, method: string) => async () => {
    switch (true) {
        case method === 'GetAboutYouPortrait':
            return current;
        case method === 'ConfirmAboutYouPortrait':
            return ['deep', 'calendar', 'meetings', 'hikes', 'trips', 'focus'];
        case method === 'NotQuiteMemoryEntry':
            return 0;
        case method === 'SaveMemoryEntry':
            return { id: 'groceries', content: '' };
        default:
            return null;
    }
} }) } };

const evidence: Record<string, PortraitEvidence> = {
    deep: { id: 'deep', text: 'Does deep work in blocks of 90 minutes or more before noon', status: 'inferred', confidence: 3, sessionCount: 14, dayCount: 9, apps: ['Cursor', 'Notion'] },
    calendar: { id: 'calendar', text: 'Keeps a calendar block for every plan', status: 'inferred', confidence: 2, sessionCount: 6, dayCount: 5, apps: ['Calendar'] },
    start: { id: 'start', text: 'Starts work at about 8 AM', status: 'confirmed', confidence: 3, sessionCount: 12, dayCount: 12, apps: [] },
    meetings: { id: 'meetings', text: 'Moves meetings to after 2 PM', status: 'inferred', confidence: 2, sessionCount: 5, dayCount: 4, apps: ['Calendar'] },
};
const describe = (id: string) => evidence[id] ?? { id, text: id, status: 'inferred', confidence: 2, sessionCount: 3, dayCount: 3, apps: [] };
await i18n.changeLanguage(params.get('language') ?? 'en');
const items: ReviewItem[] = [
    { id: 'move', text: 'Moves morning meetings to the afternoon.', where: 'Seen 4 times · Calendar', status: 'inferred', portraitCandidate: true },
    { id: 'first', text: 'Puts the answer in the first line.', where: 'How you write · Seen 12 times · Mail', status: 'inferred', communication: true },
    { id: 'groceries', text: 'Orders groceries on Sundays.', where: 'Seen 3 times · Mail', status: 'learned_today' },
    { id: 'recipes', text: 'Looks up quick recipes on weeknights.', where: 'Seen 9 times · Safari', status: 'inferred' },
];
function Cards() {
    const review = useBeliefReview({ onConfirmed: noop, onForgotten: noop, onChanged: noop, contentOf: () => '# Belief\n\n## Memory Points\n\n- Belief.\n' });
    return <NewThisWeek items={items} review={review} />;
}
const card = { renamed: false, name: ['blueprint', 'builder'], sides: [0.17, 0.14, 0.25, 0.14], sentence: current.sentences?.[0]?.parts ?? [] };

createRoot(document.getElementById('root')!).render(
    <main className="min-h-screen bg-background text-ink" data-testid="portrait-fixture">
        <div className="mx-auto max-w-[760px] px-6 py-6">
            {state === 'today'
                ? <TodayPortraitCard card={card} onRead={noop} onDismiss={noop} />
                : state === 'cards'
                ? <Cards />
                : <AboutYouPortraitCard describe={describe} onShowBelief={noop} onNotQuiteBelief={noop} />}
        </div>
    </main>,
);
