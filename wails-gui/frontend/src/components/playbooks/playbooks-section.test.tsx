import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { pageQueryCache } from '@/lib/query-cache';
import type { PlaybookView, PlaybooksState, SopEntry } from '@/lib/wails-shim-app';
import { KnowledgePage } from '@/components/knowledge-page';
import { PlaybookCards } from './PlaybookCards';
import { listConfirmedPlaybooks, listDraftPlaybooks } from './playbook-lists';
import { formatRunDay, playbookRunWhen } from './playbook-format';

const mocks = vi.hoisted(() => ({
    listKnowHow: vi.fn(),
    getMatch: vi.fn(),
    getCandidate: vi.fn(),
    getEntries: vi.fn(),
    listSops: vi.fn(),
    getPlaybooks: vi.fn(),
    getAuthor: vi.fn(),
    runSop: vi.fn(),
    runNow: vi.fn(),
    getScript: vi.fn(),
    toastSuccess: vi.fn(),
    toastError: vi.fn(),
}));

vi.mock('@/lib/wails-shim-app', () => ({
    IdeaMeaningGroups: () => Promise.resolve(null),
    ListKnowHow: mocks.listKnowHow,
    GetKnowHowMatch: mocks.getMatch,
    GetKnowHowCandidate: mocks.getCandidate,
    GetKnowledgeEntries: mocks.getEntries,
    ListSOPs: mocks.listSops,
    GetPlaybooksState: mocks.getPlaybooks,
    GetSOPAuthorStatus: mocks.getAuthor,
    RunSOP: mocks.runSop,
    RunPlaybookNow: mocks.runNow,
    GetSOPScript: mocks.getScript,
}));
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {} }));
vi.mock('@/lib/notify', () => ({ notifyToast: vi.fn() }));
vi.mock('sonner', () => ({ toast: { success: mocks.toastSuccess, error: mocks.toastError } }));

const DAY = 86_400_000;
const now = Date.now();

const WEEKLY: SopEntry = {
    id: 'pb-weekly',
    title: 'Weekly update',
    description: 'Monday notes into the update, in your words.',
    trigger: 'Mondays',
    status: 'confirmed',
    source: 'manual',
    sourceIds: [],
    version: 2,
    createdAt: now - 30 * DAY,
    updatedAt: now - 1_000,
    scripts: [],
    sites: [],
    body: '# Steps\n\nRead the notes.',
};
const RELEASE: SopEntry = {
    ...WEEKLY,
    id: 'pb-release',
    title: 'Release notes',
    description: 'From merged pull requests, two sentences each.',
    trigger: '',
    updatedAt: now - 2_000,
    body: 'Write the notes.',
};
const DRAFT: SopEntry = {
    ...WEEKLY,
    id: 'pb-draft',
    title: 'Friday invoice run',
    description: 'Same five steps every Friday.',
    status: 'draft',
    source: 'observed',
    updatedAt: now,
};
const OFFERED: SopEntry = {
    ...DRAFT,
    id: 'pb-offered',
    title: 'Offered routine',
    description: 'Isoform noticed this on your Mac.',
    updatedAt: now - 3_000,
};

const SCHEDULED_PLAN: PlaybookView = {
    id: 'pb-weekly',
    proposal: null,
    schedule: { jobId: 'job-1', time: '09:00', weekdays: true, enabled: true },
    channels: ['desktop'],
    sites: [],
    lastRun: { runId: 'run-1', at: now - DAY, ok: true, result: 'Done' },
    nextRunAt: 0,
    suggestedTime: '',
    held: '',
};
const RELEASE_PLAN: PlaybookView = {
    ...SCHEDULED_PLAN,
    id: 'pb-release',
    schedule: null,
    lastRun: null,
};
const OFFER_PLAN: PlaybookView = {
    ...SCHEDULED_PLAN,
    id: 'pb-offered',
    schedule: null,
    lastRun: null,
    proposal: {
        host: 'example.com',
        siteName: 'Example',
        time: '09:00',
        days: 5,
        weekdays: true,
        titles: [],
        proposedAt: now,
        answer: '',
        answeredAt: 0,
    },
};

const STATE: PlaybooksState = {
    plans: [SCHEDULED_PLAN, RELEASE_PLAN, OFFER_PLAN],
    channels: { desktop: true, push: false, text: false, email: false, textNames: false },
    defaultChannels: ['desktop'],
};

function setLibrary(sops: SopEntry[], state: PlaybooksState = STATE) {
    mocks.listSops.mockResolvedValue(sops);
    mocks.getPlaybooks.mockResolvedValue(state);
}

async function renderPage(props: Partial<React.ComponentProps<typeof KnowledgePage>> = {}) {
    const onRunPlaybook = vi.fn(async () => {});
    const onOpenPlaybookHandled = vi.fn();
    await act(async () => {
        render(
            <KnowledgePage
                onRunPlaybook={onRunPlaybook}
                onOpenPlaybookHandled={onOpenPlaybookHandled}
                {...props}
            />,
        );
    });
    return { onRunPlaybook, onOpenPlaybookHandled };
}

function cardFor(title: string): HTMLElement {
    const open = screen.getByRole('button', { name: new RegExp(`^${title}`) });
    const row = open.closest('li');
    if (!row) throw new Error(`no card row for ${title}`);
    return row;
}

describe('playbook helpers', () => {
    const sops = [WEEKLY, RELEASE, DRAFT, OFFERED];
    const state: PlaybooksState = { ...STATE, plans: [SCHEDULED_PLAN, RELEASE_PLAN, OFFER_PLAN] };

    it('lists confirmed playbooks only, newest first, leaving out open offers', () => {
        const confirmed = listConfirmedPlaybooks(sops, state).map((item) => item.entry.id);
        expect(confirmed).toEqual(['pb-weekly', 'pb-release']);
    });

    it('lists drafts and open offers as drafts, and never a confirmed playbook', () => {
        const drafts = listDraftPlaybooks(sops, state).map((item) => item.entry.id);
        expect(drafts).toEqual(['pb-draft', 'pb-offered']);
    });

    it('treats a playbook with no plan as confirmed when its status says so', () => {
        expect(listConfirmedPlaybooks([WEEKLY], null).map((item) => item.plan)).toEqual([null]);
    });

    it('describes when a playbook runs from its schedule', () => {
        expect(playbookRunWhen(SCHEDULED_PLAN)).toEqual({ kind: 'weekdays', time: '09:00' });
        expect(playbookRunWhen({ ...SCHEDULED_PLAN, schedule: { ...SCHEDULED_PLAN.schedule!, weekdays: false } })).toEqual({
            kind: 'daily',
            time: '09:00',
        });
        expect(playbookRunWhen(RELEASE_PLAN)).toEqual({ kind: 'onRequest' });
        expect(playbookRunWhen({ ...SCHEDULED_PLAN, schedule: { ...SCHEDULED_PLAN.schedule!, enabled: false } })).toEqual({
            kind: 'onRequest',
        });
    });

    it('names the day of a run as today, yesterday, a weekday or a date', () => {
        const at = new Date(2026, 9, 28, 12).getTime();
        expect(formatRunDay(at + 60_000, at + 3_600_000, 'en')).toEqual({ key: 'today', text: '' });
        expect(formatRunDay(at - DAY, at, 'en')).toEqual({ key: 'yesterday', text: '' });
        expect(formatRunDay(at - 3 * DAY, at, 'en').text).toBe(
            new Date(at - 3 * DAY).toLocaleDateString('en', { weekday: 'short' }),
        );
        expect(formatRunDay(at - 20 * DAY, at, 'en').text).toBe(
            new Date(at - 20 * DAY).toLocaleDateString('en', { month: 'short', day: 'numeric' }),
        );
    });
});

describe('PlaybookCards', () => {
    const items = [
        { entry: WEEKLY, plan: SCHEDULED_PLAN },
        { entry: RELEASE, plan: null },
    ];

    it('renders nothing when there are no confirmed playbooks', () => {
        const { container } = render(<PlaybookCards items={[]} locale="en" onOpen={vi.fn()} />);
        expect(container).toBeEmptyDOMElement();
    });

    it('shows each name, what it does, when it runs and when it last ran', () => {
        render(<PlaybookCards items={items} locale="en" onOpen={vi.fn()} onRun={vi.fn()} />);
        const weekly = cardFor('Weekly update');
        expect(within(weekly).getByText('Monday notes into the update, in your words.')).toBeInTheDocument();
        expect(within(weekly).getByText(/Weekdays at 9:00\s?AM/)).toBeInTheDocument();
        expect(within(weekly).getByText(/last run yesterday/)).toBeInTheDocument();
    });

    it('says when asked, and leaves out the last run when it never ran', () => {
        render(<PlaybookCards items={items} locale="en" onOpen={vi.fn()} onRun={vi.fn()} />);
        const release = cardFor('Release notes');
        expect(within(release).getByText(/when you ask/)).toBeInTheDocument();
        expect(within(release).queryByText(/last run/)).toBeNull();
    });

    it('opens the playbook when its name is clicked', () => {
        const onOpen = vi.fn();
        render(<PlaybookCards items={items} locale="en" onOpen={onOpen} onRun={vi.fn()} />);
        fireEvent.click(screen.getByRole('button', { name: /^Weekly update/ }));
        expect(onOpen).toHaveBeenCalledWith('pb-weekly');
    });

    it('runs the playbook when Run now is clicked, and hides Run now without a handler', () => {
        const onRun = vi.fn();
        const { unmount } = render(<PlaybookCards items={items} locale="en" onOpen={vi.fn()} onRun={onRun} />);
        fireEvent.click(within(cardFor('Weekly update')).getByRole('button', { name: 'Run now' }));
        expect(onRun).toHaveBeenCalledWith(WEEKLY);
        unmount();

        render(<PlaybookCards items={items} locale="en" onOpen={vi.fn()} />);
        expect(screen.queryByRole('button', { name: 'Run now' })).toBeNull();
    });
});

describe('How you do things: Isoform can do these for you', () => {
    beforeEach(async () => {
        vi.resetAllMocks();
        pageQueryCache.clear();
        await i18n.changeLanguage('en');
        mocks.listKnowHow.mockResolvedValue([]);
        mocks.getEntries.mockResolvedValue([]);
        mocks.getMatch.mockResolvedValue(null);
        mocks.getCandidate.mockResolvedValue(null);
        mocks.getAuthor.mockResolvedValue(null);
        mocks.getScript.mockResolvedValue('');
        setLibrary([WEEKLY, RELEASE, DRAFT, OFFERED]);
    });

    afterEach(() => {
        cleanup();
    });

    it('shows confirmed playbooks as cards and never lists a draft', async () => {
        await renderPage();
        expect(await screen.findByRole('heading', { level: 2, name: 'Isoform can do these for you' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /^Weekly update/ })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /^Release notes/ })).toBeInTheDocument();
        expect(screen.getByText('2 playbooks')).toBeInTheDocument();
        expect(screen.queryByText('Friday invoice run')).toBeNull();
        expect(screen.queryByText('Offered routine')).toBeNull();
    });

    it('hides the section when no playbook is confirmed, and keeps the page actions', async () => {
        setLibrary([DRAFT, OFFERED]);
        await renderPage();
        await screen.findByRole('button', { name: /Create playbook/ });
        expect(screen.queryByRole('heading', { name: 'Isoform can do these for you' })).toBeNull();
        expect(screen.queryByText('Friday invoice run')).toBeNull();
        expect(screen.getByRole('button', { name: /^Import$/ })).toBeInTheDocument();
    });

    it('opens the playbook detail in the main column, with Export and Import in its More menu', async () => {
        await renderPage();
        fireEvent.click(await screen.findByRole('button', { name: /^Weekly update/ }));

        expect(await screen.findByRole('heading', { level: 1, name: 'Weekly update' })).toBeInTheDocument();
        expect(screen.getByText('Read the notes.')).toBeInTheDocument();
        expect(screen.queryByRole('heading', { name: 'Isoform can do these for you' })).toBeNull();

        await act(async () => {
            fireEvent.keyDown(screen.getByRole('button', { name: 'More for this playbook' }), { key: 'Enter' });
        });
        expect(await screen.findByRole('menuitem', { name: /Export/ })).toBeInTheDocument();
        expect(screen.getByRole('menuitem', { name: /Import/ })).toBeInTheDocument();
        expect(screen.getByRole('menuitem', { name: /Delete/ })).toBeInTheDocument();

        await act(async () => {
            fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
        });
        await vi.waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
        fireEvent.click(screen.getByRole('button', { name: 'Back to How you do things' }));
        expect(await screen.findByRole('heading', { level: 2, name: 'Isoform can do these for you' })).toBeInTheDocument();
    });

    it('W11: Run now on a card runs in place and never opens Chat', async () => {
        mocks.runNow.mockResolvedValue({});
        const { onRunPlaybook } = await renderPage();
        await act(async () => {
            fireEvent.click(within(cardFor('Weekly update')).getByRole('button', { name: 'Run now' }));
        });
        expect(mocks.runNow).toHaveBeenCalledWith('pb-weekly', []);
        expect(within(cardFor('Weekly update')).getByRole('status')).toHaveTextContent('Running. Isoform will send you the result.');
        expect(within(cardFor('Weekly update')).queryByRole('button', { name: 'Run now' })).toBeNull();
        expect(onRunPlaybook).not.toHaveBeenCalled();
        expect(mocks.runSop).not.toHaveBeenCalled();
    });

    it('W11: a failed card run stays on the card in copper, and Try again runs it again', async () => {
        mocks.runNow.mockRejectedValueOnce(new Error('agent offline')).mockResolvedValue({});
        await renderPage();
        await act(async () => {
            fireEvent.click(within(cardFor('Weekly update')).getByRole('button', { name: 'Run now' }));
        });
        const card = cardFor('Weekly update');
        expect(await within(card).findByText('Didn’t run.')).toBeInTheDocument();
        expect(card.textContent).not.toContain('agent offline');
        await act(async () => {
            fireEvent.click(within(card).getByRole('button', { name: 'Try again' }));
        });
        expect(mocks.runNow).toHaveBeenCalledTimes(2);
        expect(within(cardFor('Weekly update')).getByRole('status')).toHaveTextContent('Running. Isoform will send you the result.');
    });

    it('opens a playbook requested by a notification or Search, and reports it handled', async () => {
        const { onOpenPlaybookHandled } = await renderPage({ openPlaybookId: 'pb-release', openPlaybookSignal: 1 });
        expect(await screen.findByRole('heading', { level: 1, name: 'Release notes' })).toBeInTheDocument();
        expect(onOpenPlaybookHandled).toHaveBeenCalledTimes(1);
    });

    it('never opens a draft from an id, even when one is asked for', async () => {
        const { onOpenPlaybookHandled } = await renderPage({ openPlaybookId: 'pb-draft', openPlaybookSignal: 1 });
        await screen.findByRole('heading', { level: 2, name: 'Isoform can do these for you' });
        expect(screen.queryByRole('heading', { level: 1, name: 'Friday invoice run' })).toBeNull();
        expect(onOpenPlaybookHandled).toHaveBeenCalledTimes(1);
    });
});
