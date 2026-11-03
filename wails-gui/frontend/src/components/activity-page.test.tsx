import { resetActivitySessionCache } from './timeline/activity-session-cache';
import { resetPageQueries } from '@/lib/page-queries';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import enActivity from '@/i18n/locales/en/activity.json';
import { ActivityPage, SessionPeopleChips, SessionSpeakerChips, type ActivitySectionId } from './activity-page';
import type { CronJobsProp } from './timeline/useCalendarData';
import type { MobileBriefActivityTarget } from '@/lib/wails-shim-app';

// Every Wails binding resolves through the shim (vite aliases wailsjs/go/main/App
// to it), so one Proxy mock covers the whole import graph of the page — same
// pattern as activity-day-view.test.tsx.
const fixture = vi.hoisted(() => ({
    bindings: new Map<string, ReturnType<typeof vi.fn>>(),
    listeners: new Map<string, Set<(payload: unknown) => void>>(),
}));

vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        if (!fixture.bindings.has(name)) fixture.bindings.set(name, vi.fn().mockResolvedValue([]));
        return fixture.bindings.get(name);
    },
}));

vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, handler: (payload: unknown) => void) => {
        const listeners = fixture.listeners.get(name) ?? new Set();
        listeners.add(handler);
        fixture.listeners.set(name, listeners);
        return () => listeners.delete(handler);
    },
    ClipboardSetText: vi.fn(),
}));

vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));

function binding(name: string) {
    if (!fixture.bindings.has(name)) fixture.bindings.set(name, vi.fn().mockResolvedValue([]));
    return fixture.bindings.get(name)!;
}

const cronJobs: CronJobsProp = {
    jobs: [],
    loading: false,
    toggleEnabled: vi.fn(),
    deleteJob: vi.fn(),
    runNow: vi.fn(),
    updateJob: vi.fn(),
    loadRuns: vi.fn(),
    loadRunLogs: vi.fn(),
};

/** Harness holding the section state the way App does: the page is
 *  controlled, and the pick is remembered outside it. */
function Harness({ initialSection = 'yourDay' as ActivitySectionId, initialDate = null as string | null, initialFocus = null as MobileBriefActivityTarget | null }) {
    const [section, setSection] = useState<ActivitySectionId>(initialSection);
    const [focus, setFocus] = useState(initialFocus);
    return (
        <ActivityPage
            initialDate={initialDate}
            noticedFocus={focus}
            onNoticedFocusConsumed={() => setFocus(null)}
            cronJobs={cronJobs}
            section={section}
            onSectionChange={setSection}
        />
    );
}

describe('ActivityPage section panel', () => {
    beforeEach(async () => {
        resetActivitySessionCache();
        resetPageQueries();
        await i18n.changeLanguage('en');
        localStorage.clear();
        fixture.listeners.clear();
        for (const mock of fixture.bindings.values()) mock.mockReset().mockResolvedValue([]);
        Element.prototype.scrollIntoView = vi.fn();
        // The timeline detail measures its scroll area.
        globalThis.ResizeObserver ??= class {
            observe() {}
            unobserve() {}
            disconnect() {}
        } as unknown as typeof ResizeObserver;
    });

    it.each(['paper', 'onedark'])('exposes the shared app ground through the whole Activity page in %s', async (theme) => {
        document.documentElement.dataset.theme = theme;
        let container!: HTMLElement;
        await act(async () => { ({ container } = render(<main className="patina-ground"><Harness /></main>)); });
        const ground = container.querySelector('main')!;
        const page = ground.firstElementChild!;
        expect(ground).toHaveClass('patina-ground');
        expect(page).not.toHaveClass('bg-background', 'bg-panel', 'patina-ground');
        expect((page as HTMLElement).style.backgroundColor).toBe('');
        expect(ground.querySelector('.patina-ground')).toBeNull();
    });

    it.each([true, false])('uses neutral ink when selecting a person (self: %s)', (self) => {
        const onSelect = vi.fn();
        const name = self ? 'You' : 'Alex';
        render(<SessionPeopleChips principals={[{ principalId: 'person', displayName: name, kind: self ? 'self' : 'person', qualifiedDisplayName: name, firstSeenAt: 0, lastSeenAt: 1 }]} selectedName={name} onSelect={onSelect} />);
        const selected = screen.getByRole('button', { name, pressed: true });
        expect(selected).toHaveClass('bg-surface-active', 'text-ink', 'ring-accent');
        expect(selected).not.toHaveClass('bg-accent-2', 'text-solid-btn-fg');
        fireEvent.click(selected);
        expect(onSelect).toHaveBeenCalledWith(name);
    });

    it.each([true, false])('uses neutral ink when selecting a voice (self: %s)', (self) => {
        const onSelect = vi.fn();
        const label = self ? 'You' : 'Speaker 1';
        render(<SessionSpeakerChips clusters={[{ label, isSelf: self, utteranceCount: 3, firstSeenAt: 0, lastSeenAt: 1, eventTimestamps: [] }]} selectedLabel={label} onSelect={onSelect} />);
        const selected = screen.getByRole('button', { pressed: true });
        expect(selected).toHaveClass('bg-surface-active', 'text-ink', 'ring-accent');
        expect(selected).not.toHaveClass('bg-accent-2', 'text-solid-btn-fg');
        fireEvent.click(selected);
        expect(onSelect).toHaveBeenCalledWith(label);
    });

    it('shows one section at a time, and the section rows pick it', async () => {
        await act(async () => {
            render(<Harness initialSection="yourDay" />);
        });
        // Your day shows; the other day sections do not.
        expect(screen.getByRole('heading', { name: enActivity.day.yourDay })).toBeInTheDocument();
        expect(screen.queryByRole('heading', { name: enActivity.day.workedOn })).toBeNull();

        // Clicking "What you worked on" swaps the section.
        fireEvent.click(document.querySelector('[data-activity-section="workedOn"]') as HTMLElement);
        await waitFor(() => {
            expect(screen.queryByRole('heading', { name: enActivity.day.yourDay })).toBeNull();
            expect(screen.getByRole('heading', { name: enActivity.day.workedOn })).toBeInTheDocument();
        });
        // The section row shows the current pick.
        expect((document.querySelector('[data-activity-section="workedOn"]') as HTMLElement).getAttribute('aria-current')).toBe('page');
    });

    it('does not load the Screen timeline until its section is picked', async () => {
        await act(async () => {
            render(<Harness initialSection="yourDay" />);
        });
        // The heavy timeline queries wait for the timeline section.
        expect(binding('GetActivitySessions')).not.toHaveBeenCalled();
        expect(binding('GetActivityTimelineGroups')).not.toHaveBeenCalled();

        fireEvent.click(document.querySelector('[data-activity-section="timeline"]') as HTMLElement);
        await waitFor(() => {
            expect(binding('GetActivitySessions')).toHaveBeenCalled();
            expect(binding('GetActivityTimelineGroups')).toHaveBeenCalled();
        });
    });

    it('opens on the day the caller asked for', async () => {
        await act(async () => {
            render(<Harness initialDate="2026-10-15" />);
        });
        // The date navigation in the panel names the picked day (Oct 15, 2026
        // was a Thursday), not today.
        expect(screen.getByTitle(enActivity.dayNav.pickDate)).toHaveTextContent('Oct 15');
    });

    it('opens straight on the Screen timeline for a session jump', async () => {
        await act(async () => {
            render(<Harness initialSection="timeline" />);
        });
        await waitFor(() => {
            expect(binding('GetActivityTimelineGroups')).toHaveBeenCalled();
        });
        // The day section rows and the date navigation hide on the timeline.
        expect(document.querySelector('[data-activity-section="workedOn"]')).not.toBeNull();
        expect(screen.queryByTitle(enActivity.dayNav.pickDate)).toBeNull();
    });

    it.each(['follow_up', 'routine'] as const)('opens the focused %s editor inside Your day without saving', async (action) => {
        const focus = { date: '2026-10-15', suggestionId: 'suggestion-1', action };
        binding('GetDayNoticed').mockResolvedValue([{
            id: focus.suggestionId, kind: action, text: 'Follow up on the review', why: 'A decision is waiting.',
            momentId: 'moment-1', momentTitle: 'Reviewing the plan', momentStartMs: new Date(2026, 9, 15, 10).getTime(),
            routine: { name: 'Weekly review', when: 'weekly fri 16:00', steps: ['Read the plan'], will: ['Prepare notes'], wont: ['Send anything'] },
        }]);
        render(<Harness initialDate={focus.date} initialFocus={focus} />);
        expect(await screen.findByRole('heading', { name: enActivity.noticed.title })).toBeInTheDocument();
        expect(binding('GetDayNoticed')).toHaveBeenCalledWith(focus.date);
        if (action === 'follow_up') {
            expect(await screen.findByRole('textbox', { name: 'Follow-up' })).toHaveValue('Follow up on the review');
        } else {
            const dialog = await screen.findByRole('dialog');
            fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
        }
        expect(binding('AddActivityFollowUp')).not.toHaveBeenCalled();
        expect(binding('SaveNoticedRoutine')).not.toHaveBeenCalled();
        expect(binding('GetActivitySessions')).not.toHaveBeenCalled();
        fireEvent.click(document.querySelector('[data-activity-section="workedOn"]') as HTMLElement);
        fireEvent.click(document.querySelector('[data-activity-section="yourDay"]') as HTMLElement);
        expect(await screen.findByRole('heading', { name: enActivity.noticed.title })).toBeInTheDocument();
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
        expect(screen.queryByRole('textbox', { name: 'Follow-up' })).not.toBeInTheDocument();
    });

    it.each([false, true])('reveals a suggestion source even before moments and threads load (threaded: %s)', async (threaded) => {
        const startMs = new Date(2026, 9, 15, 10).getTime();
        const moment = {
            id: 'moment-1', status: 'settled', title: 'Reviewing the plan', description: 'The review details.',
            startMs, endMs: startMs + 60_000, activeMs: 60_000, category: 'build', categoryColor: '',
            entities: [], apps: [], thumbnails: [], runCount: 1, inProgress: false,
        };
        let finishMoments!: (rows: unknown[]) => void;
        let finishThreads!: (rows: unknown) => void;
        binding('GetActivitiesInRange').mockReturnValue(new Promise((resolve) => { finishMoments = resolve; }));
        binding('GetDayThreads').mockReturnValue(new Promise((resolve) => { finishThreads = resolve; }));
        const groups = { grouped: threaded, threads: threaded ? [{
            id: 'thread-1', title: 'Planning', kind: 'project', userTitled: false,
            startMs, endMs: startMs + 60_000, timeSpentMs: 60_000, activityIds: ['moment-1', 'moment-2'],
        }] : [] };
        binding('GetDayNoticed').mockResolvedValue([{
            id: 'suggestion-1', kind: 'follow_up', text: 'Follow up on the review', why: '',
            momentId: moment.id, momentTitle: moment.title, momentStartMs: startMs,
        }]);
        render(<Harness initialDate="2026-10-15" />);
        const source = await screen.findByRole('button', { name: /From .*Reviewing the plan/ });
        fireEvent.click(source);
        expect(document.querySelector('[data-activity-section="workedOn"]')).toHaveAttribute('aria-current', 'page');
        await act(async () => {
            finishMoments(threaded ? [moment, { ...moment, id: 'moment-2', title: 'Preparing notes' }] : [moment]);
        });
        await act(async () => { finishThreads(groups); });
        expect(await screen.findByRole('button', { name: 'Delete from history' })).toBeInTheDocument();
        expect(document.querySelector('[data-activity-section="workedOn"]')).toHaveAttribute('aria-current', 'page');
        expect(screen.queryByRole('heading', { name: enActivity.noticed.title })).not.toBeInTheDocument();
        expect(binding('GetActivitySessions')).not.toHaveBeenCalled();
        fireEvent.click(screen.getByText(moment.title).closest('button')!);
        expect(screen.queryByRole('button', { name: 'Delete from history' })).not.toBeInTheDocument();
        binding('GetActivitiesInRange').mockResolvedValue(threaded ? [{ ...moment }, { ...moment, id: 'moment-2', title: 'Preparing notes' }] : [{ ...moment }]);
        binding('GetDayThreads').mockResolvedValue(groups);
        await act(async () => {
            fixture.listeners.get('activity:activities-updated')?.forEach((handler) => handler(undefined));
        });
        expect(screen.queryByRole('button', { name: 'Delete from history' })).not.toBeInTheDocument();
    });
});

describe('Activity first view and session reuse', () => {
    beforeEach(async () => {
        resetActivitySessionCache();
        resetPageQueries();
        fixture.listeners.clear();
        for (const mock of fixture.bindings.values()) mock.mockReset().mockResolvedValue([]);
        await i18n.changeLanguage('en');
    });

    it('renders rows before the digest and libraries resolve, then keeps the loaded sections', async () => {
        let finishSummary!: (value: unknown) => void;
        let finishKnowledge!: (value: unknown) => void;
        let finishMemory!: (value: unknown) => void;
        binding('EnsureDailySummary').mockReturnValue(new Promise((resolve) => { finishSummary = resolve; }));
        binding('GetKnowledgeEntries').mockReturnValue(new Promise((resolve) => { finishKnowledge = resolve; }));
        binding('GetMemoryEntries').mockReturnValue(new Promise((resolve) => { finishMemory = resolve; }));
        binding('GetActivitiesInRange').mockResolvedValue([{
            id: 'fast-row', title: 'Public fixture row', startMs: new Date(2026, 9, 15, 9).getTime(), endMs: new Date(2026, 9, 15, 10).getTime(),
            apps: [], entities: [], thumbnails: [], runCount: 0, activeMs: 60_000,
        }]);
        render(<Harness initialDate="2026-10-15" />);
        expect(await screen.findByText('Public fixture row')).toBeInTheDocument();
        expect(binding('GetKnowledgeEntries')).not.toHaveBeenCalled();
        expect(binding('GetWeeklySummary')).not.toHaveBeenCalled();
        expect(binding('GetActivityMonthStats')).not.toHaveBeenCalled();
        await act(async () => finishSummary({ aiSummary: 'Fixture digest', hourlyActive: [], appBreakdown: [] }));
        expect(await screen.findByText('Fixture digest')).toBeInTheDocument();
        fireEvent.click(document.querySelector('[data-activity-section="learned"]')!);
        expect(screen.queryByRole('heading', { name: enActivity.day.learned })).not.toBeInTheDocument();
        await act(async () => { finishKnowledge([{ id: 'fixture-knowledge', title: 'Fixture learning', date: '2026-10-15', content: 'Public content' }]); finishMemory([]); });
        expect(await screen.findByRole('heading', { name: enActivity.day.learned })).toBeInTheDocument();
        expect(screen.getByText('Fixture learning')).toBeInTheDocument();
    });

    it('restores the selected day and its rows synchronously while revalidating', async () => {
        const startMs = new Date(2026, 9, 15, 9).getTime();
        binding('GetActivitiesInRange').mockResolvedValue([{
            id: 'kept-row', title: 'Kept public fixture row', startMs, endMs: startMs + 300_000, apps: [], entities: [], thumbnails: [], runCount: 0, activeMs: 300_000,
        }]);
        binding('GetDayThreads').mockResolvedValue({ grouped: true, threads: [{
            id: 'kept-thread', title: 'Kept public thread', kind: 'work', userTitled: false, startMs, endMs: startMs + 300_000,
            timeSpentMs: 300_000, activityIds: ['kept-row'], activeMs: 300_000, gaps: [], interruption: false, reviewed: false, unfinished: false,
            stretches: [{ start: startMs, end: startMs + 300_000, activeMs: 300_000, activityIds: ['kept-row'], resumePoint: null }],
            linkReason: { kind: 'rule', keys: [], text: '' },
        }] });
        const first = render(<Harness initialSection="workedOn" initialDate="2026-10-15" />);
        // Stitched by work first, then the same rows as recorded.
        expect((await screen.findAllByText('Kept public thread')).length).toBeGreaterThan(0);
        fireEvent.click(document.querySelector('[data-worked-on-view="recorded"]') as HTMLElement);
        await screen.findByText('Kept public fixture row');
        first.unmount();
        binding('GetActivitiesInRange').mockReturnValue(new Promise(() => {}));
        binding('GetDayThreads').mockReturnValue(new Promise(() => {}));
        render(<Harness initialSection="workedOn" />);
        expect(screen.getAllByText('Kept public thread').length).toBeGreaterThan(0);
        fireEvent.click(document.querySelector('[data-worked-on-view="recorded"]') as HTMLElement);
        expect(screen.getByText('Kept public fixture row')).toBeInTheDocument();
        expect(binding('GetActivitiesInRange')).toHaveBeenLastCalledWith(new Date(2026, 9, 15).getTime(), new Date(2026, 9, 16).getTime());
    });
});

it('settles a failed digest and thread read so the existing summary and source controls remain usable', async () => {
    await i18n.changeLanguage('en');
    resetActivitySessionCache();
    fixture.listeners.clear();
    for (const mock of fixture.bindings.values()) mock.mockReset().mockResolvedValue([]);
    binding('EnsureDailySummary').mockRejectedValue(new Error('temporary read failure'));
    binding('GetDayThreads').mockRejectedValue(new Error('temporary read failure'));
    binding('GetActivitiesInRange').mockResolvedValue([{
        id: 'recoverable-row', title: 'Recoverable public row', startMs: new Date(2026, 9, 15, 9).getTime(), endMs: new Date(2026, 9, 15, 10).getTime(),
        apps: [], entities: [], thumbnails: [], runCount: 0, activeMs: 60000,
    }]);
    binding('GetDayNoticed').mockResolvedValue([{
        id: 'source', kind: 'follow_up', momentId: 'recoverable-row', momentTitle: 'Recoverable public row', momentStartMs: new Date(2026, 9, 15, 9).getTime(), text: 'Check the public row', why: '',
    }]);
    render(<Harness initialDate="2026-10-15" />);
    expect(await screen.findByRole('button', { name: enActivity.day.writeSummary })).toBeEnabled();
    fireEvent.click(await screen.findByRole('button', { name: /From .*Recoverable public row/ }));
    expect(await screen.findByRole('button', { name: 'Delete from history' })).toBeInTheDocument();
});
