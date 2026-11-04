import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { resetPageQueries } from '@/lib/page-queries';
import type { AboutYouPortrait } from '@/lib/wails-shim-app';
import { AboutYouPortraitCard, type PortraitEvidence } from './portrait';

const mocks = vi.hoisted(() => ({
    getPortrait: vi.fn(),
    retry: vi.fn(),
    confirmPortrait: vi.fn(),
    undo: vi.fn(),
    notQuite: vi.fn(),
    seen: vi.fn(),
    confirmEntry: vi.fn(),
    answer: vi.fn(),
    undoRow: vi.fn(),
    showPending: vi.fn(),
    undoEntry: vi.fn(),
    emit: vi.fn(),
    copy: vi.fn(),
    events: new Map<string, Set<(value: unknown) => void>>(),
}));

vi.mock('@/lib/wails-shim-app', () => ({
    GetAboutYouPortrait: mocks.getPortrait,
    RetryAboutYouPortrait: mocks.retry,
    ConfirmAboutYouPortrait: mocks.confirmPortrait,
    UndoConfirmAboutYouPortrait: mocks.undo,
    NotQuiteAboutYouPortrait: mocks.notQuite,
    MarkAboutYouPortraitSeen: mocks.seen,
    ConfirmMemoryEntry: mocks.confirmEntry,
    AnswerAboutYouPortraitBelief: mocks.answer,
    UndoAboutYouPortraitBelief: mocks.undoRow,
    ShowAboutYouPortraitPending: mocks.showPending,
    UndoConfirmMemoryEntry: mocks.undoEntry,
}));
vi.mock('@/components/activity-summary', () => ({ copyTextToClipboard: mocks.copy }));
vi.mock('../../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, callback: (value: unknown) => void) => {
        const listeners = mocks.events.get(name) ?? new Set<(value: unknown) => void>();
        listeners.add(callback);
        mocks.events.set(name, listeners);
        return () => { listeners.delete(callback); };
    },
    EventsEmit: mocks.emit,
}));

async function emitEvent(name: string, payload?: unknown) {
    await act(async () => {
        for (const handler of [...(mocks.events.get(name) ?? [])]) await handler(payload);
    });
}

const now = Date.now();

function portrait(overrides: Partial<AboutYouPortrait> = {}): AboutYouPortrait {
    return {
        state: 'ready',
        name: ['blueprint', 'builder'],
        nameAreas: [['work', 'routines'], ['communication']],
        sides: [0.24, 0.2, 0.16, 0.38],
        sentences: [
            { role: 'connect', changed: false, parts: [{ text: 'You ', confirmed: false }, { text: 'plan the whole thing', memoryIds: ['plan', 'outline'], confirmed: false }, { text: ', then build it.', confirmed: false }] },
            { role: 'insight', changed: false, parts: [{ text: 'Your ', confirmed: false }, { text: 'yeses go out in about 20 minutes', memoryIds: ['yes'], confirmed: true }, { text: '.', confirmed: false }] },
            { role: 'action', changed: false, parts: [{ text: 'So Isoform ', confirmed: false }, { text: 'keeps drafts short', memoryIds: ['short', 'first'], confirmed: false }, { text: '.', confirmed: false }] },
        ],
        lately: {
            parts: [{ text: 'One deep morning a week this month. You usually have three: ', confirmed: false }, { text: 'meetings have been moving before noon', memoryIds: ['deep'], confirmed: false }, { text: '.', confirmed: false }],
            memoryId: 'deep',
            thisMonth: 1,
            usual: 3,
        },
        workWithMe: [
            { text: 'Send the number, not the opinion.', memoryIds: ['plan', 'outline'], area: 'work', source: 'slack', confirmed: false },
            { text: "Short replies aren't cold.", memoryIds: ['yes'], area: 'communication', confirmed: true },
        ],
        beliefCount: 59,
        generatedAt: now,
        writing: false,
        rewriting: false,
        forgotten: false,
        assistant: 'claude-code',
        candidates: [],
        confirming: false,
        ...overrides,
    };
}

const evidence: Record<string, PortraitEvidence> = {
    plan: { id: 'plan', text: 'Outlines the whole plan before starting', status: 'inferred', confidence: 3, sessionCount: 11, dayCount: 9, apps: ['Keynote', 'Cursor'] },
    outline: { id: 'outline', text: 'Writes headings before any text', status: 'learned_today', confidence: 2, sessionCount: 1, dayCount: 1, apps: [] },
    yes: { id: 'yes', text: 'Says yes within about 20 minutes', status: 'confirmed', confidence: 4, sessionCount: 6, dayCount: 4, apps: [] },
    deep: { id: 'deep', text: 'Deep work before noon', status: 'inferred', confidence: 3, sessionCount: 20, dayCount: 12, apps: [] },
};

function renderCard() {
    const onShowBelief = vi.fn();
    const onNotQuiteBelief = vi.fn();
    const view = render(<AboutYouPortraitCard describe={(id) => evidence[id]} onShowBelief={onShowBelief} onNotQuiteBelief={onNotQuiteBelief} />);
    return { ...view, onShowBelief, onNotQuiteBelief };
}

describe('Your portrait', () => {
    beforeEach(async () => {
        vi.resetAllMocks();
        resetPageQueries();
        mocks.events.clear();
        await i18n.changeLanguage('en');
        mocks.seen.mockResolvedValue(undefined);
        mocks.retry.mockResolvedValue(undefined);
        mocks.notQuite.mockResolvedValue(undefined);
        mocks.undo.mockResolvedValue(undefined);
        mocks.confirmEntry.mockResolvedValue({});
        mocks.answer.mockResolvedValue(undefined);
        mocks.undoRow.mockResolvedValue(undefined);
        mocks.showPending.mockResolvedValue(undefined);
        mocks.undoEntry.mockResolvedValue(undefined);
    });
    afterEach(() => {
        vi.useRealTimers();
    });

    it('Still learning: a dashed ring, the days left and the progress bar; no How to work with me', async () => {
        mocks.getPortrait.mockResolvedValue(portrait({
            state: 'learning', name: null, sentences: [], workWithMe: [], lately: null, generatedAt: 0,
            progress: { beliefs: 14, beliefsNeeded: 20, days: 3, daysNeeded: 5, groups: 2, groupsNeeded: 3, moreDays: 3, met: false, sidesReady: false },
        }));
        renderCard();
        expect(await screen.findByRole('heading', { name: 'On its way' })).toBeInTheDocument();
        // v1.0.2 Y34: one line about days, no belief counts; the bar measures days only.
        expect(screen.getByText('Isoform writes your portrait after about 3 more days of your work.')).toBeInTheDocument();
        expect(document.body).not.toHaveTextContent(/14|20 beliefs|of 5 days/);
        expect(screen.getByTestId('portrait-progress')).toHaveStyle({ transform: 'scaleX(0.6)' });
        expect(screen.getByTestId('portrait-shape')).toHaveAttribute('data-shape', 'ring');
        expect(screen.queryByText('How to work with me')).not.toBeInTheDocument();
        expect(screen.queryByText('Why this name')).not.toBeInTheDocument();
    });

    it('Writing the first: the real shape, wordless lines and who is writing; no spinner or Loading', async () => {
        mocks.getPortrait.mockResolvedValue(portrait({ state: 'writing', writing: true, name: null, sentences: [], workWithMe: [], lately: null }));
        const { container } = renderCard();
        expect(await screen.findByRole('heading', { name: 'Writing your portrait' })).toBeInTheDocument();
        expect(screen.getByText("Claude Code is writing it now, about a minute. You'll get a notification.")).toBeInTheDocument();
        expect(screen.getByTestId('portrait-shape')).toHaveAttribute('data-shape', 'figure');
        expect(container.textContent).not.toMatch(/Loading/);
        // It becomes Ready by itself when the write lands.
        mocks.getPortrait.mockResolvedValue(portrait());
        await emitEvent('portrait:updated');
        expect(await screen.findByRole('heading', { name: 'The Blueprint Builder' })).toBeInTheDocument();
        expect(screen.queryByText('A new version is ready.')).not.toBeInTheDocument();
    });

    it('a phrase wraps like the words around it and opens from the keyboard', async () => {
        mocks.getPortrait.mockResolvedValue(portrait());
        renderCard();
        const phrase = await screen.findByRole('button', { name: 'plan the whole thing' });
        // A <button> is inline-block and would push each sentence onto its own line.
        expect(phrase.tagName).toBe('SPAN');
        expect(phrase).toHaveAttribute('tabindex', '0');
        fireEvent.keyDown(phrase, { key: 'Enter' });
        expect(await screen.findByRole('dialog')).toHaveTextContent('Outlines the whole plan before starting');
    });

    it('Ready: name, underlines by confirmation, legend, meta, Lately and How to work with me', async () => {
        mocks.getPortrait.mockResolvedValue(portrait());
        renderCard();
        expect(await screen.findByRole('heading', { name: 'The Blueprint Builder' })).toBeInTheDocument();
        expect(screen.getByTestId('portrait-paragraph')).toHaveTextContent('You plan the whole thing, then build it. Your yeses go out in about 20 minutes. So Isoform keeps drafts short.');
        expect(screen.getByRole('button', { name: 'plan the whole thing' })).toHaveAttribute('data-underline', 'dotted');
        expect(screen.getByRole('button', { name: 'yeses go out in about 20 minutes' })).toHaveAttribute('data-underline', 'solid');
        expect(screen.getByText('You confirmed it')).toBeInTheDocument();
        expect(screen.getByText('Isoform inferred it')).toBeInTheDocument();
        expect(screen.getByText('Updated today')).toBeInTheDocument();
        expect(screen.getByText('Lately')).toBeInTheDocument();
        expect(screen.getByText('How to work with me')).toBeInTheDocument();
        expect(screen.getByText('From your Slack messages')).toBeInTheDocument();
        expect(screen.getByText('You confirmed this')).toBeInTheDocument();
    });

    it('names a How to work with me source only when it is a real one (Y29)', async () => {
        mocks.getPortrait.mockResolvedValue(portrait({
            workWithMe: [
                { text: 'Send the number, not the opinion.', memoryIds: ['plan'], area: 'other', confirmed: false },
                { text: 'Ask before Friday.', memoryIds: ['outline'], area: 'work', source: 'activity', confirmed: false },
            ],
        }));
        renderCard();
        expect(await screen.findByText('Ask before Friday.')).toBeInTheDocument();
        expect(screen.queryByText(/From everything else|From how you work|From your/)).toBeNull();
    });

    it('Why this name: one row per word with what it means and where it comes from', async () => {
        mocks.getPortrait.mockResolvedValue(portrait());
        renderCard();
        fireEvent.click(await screen.findByRole('button', { name: 'Why this name' }));
        const popover = await screen.findByRole('dialog');
        expect(popover).toHaveTextContent('Blueprint: you plan ahead and reason from first principles');
        expect(popover).toHaveTextContent('From how you work and your routines');
        expect(popover).toHaveTextContent('Builder: you go deep on one thing and keep your words brief');
    });

    it('a phrase opens its beliefs with status, confidence and how often they were seen', async () => {
        mocks.getPortrait.mockResolvedValue(portrait());
        const { onShowBelief, onNotQuiteBelief } = renderCard();
        fireEvent.click(await screen.findByRole('button', { name: 'plan the whole thing' }));
        const popover = await screen.findByRole('dialog');
        expect(popover).toHaveTextContent('Why Isoform says this');
        expect(popover).toHaveTextContent('Outlines the whole plan before starting');
        expect(popover).toHaveTextContent('Inferred');
        expect(popover).toHaveTextContent('Confidence 3 of 4');
        expect(popover).toHaveTextContent('Seen 11 times on 9 days · Keynote, Cursor');
        expect(popover).not.toHaveTextContent('Learned today');
        fireEvent.click(within(popover).getByRole('button', { name: "That's me" }));
        await act(async () => {});
        expect(mocks.confirmEntry.mock.calls.map((call) => call[0])).toEqual(['plan', 'outline']);
        // The popover stays open with the answer and Undo; the underline draws solid behind it.
        expect(within(popover).getByRole('status')).toHaveTextContent('Confirmed');
        const phrase = screen.getByRole('button', { name: 'plan the whole thing' });
        expect(phrase).toHaveAttribute('data-underline', 'solid');
        expect(phrase).toHaveClass('answer-underline-draw');
        fireEvent.click(within(popover).getByRole('button', { name: 'Undo' }));
        await act(async () => {});
        expect(mocks.undoEntry.mock.calls.map((call) => call[0])).toEqual(['plan', 'outline']);
        expect(phrase).toHaveAttribute('data-underline', 'dotted');
        fireEvent.click(within(popover).getByRole('button', { name: 'Not quite' }));
        expect(onNotQuiteBelief).toHaveBeenCalledWith('plan');
        fireEvent.click(screen.getByRole('button', { name: 'plan the whole thing' }));
        fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Show in Beliefs' }));
        expect(onShowBelief).toHaveBeenCalledWith('plan');
    });

    it('the Lately phrase opens its facts, with no buttons', async () => {
        mocks.getPortrait.mockResolvedValue(portrait());
        renderCard();
        fireEvent.click(await screen.findByRole('button', { name: 'meetings have been moving before noon' }));
        const popover = await screen.findByRole('dialog');
        expect(popover).toHaveTextContent('1 a week this month · usually 3');
        expect(within(popover).queryByRole('button')).not.toBeInTheDocument();
    });

    // About you v1.0.0 Y14: zh learning heading.
    it('reads 还在了解你 while still learning in zh-CN', async () => {
        await i18n.changeLanguage('zh-CN');
        mocks.getPortrait.mockResolvedValue(portrait({
            state: 'learning', name: null, sentences: [], workWithMe: [], lately: null, generatedAt: 0,
            progress: { beliefs: 14, beliefsNeeded: 20, days: 3, daysNeeded: 5, groups: 2, groupsNeeded: 3, moreDays: 3, met: false, sidesReady: false },
        }));
        renderCard();
        expect(await screen.findByRole('heading', { name: '还在了解你' })).toBeInTheDocument();
        await i18n.changeLanguage('en');
    });

    // About you v1.0.0 Y12: no empty box; a cold read that failed says so in copper.
    it.each([
        ['en', 'Your portrait', "Couldn't open your portrait.", 'Try again'],
        ['zh-CN', null, '没能打开你的画像。', null],
    ] as const)('a portrait that could not open says so with Try again in %s', async (language, heading, line, retry) => {
        await i18n.changeLanguage(language);
        mocks.getPortrait.mockRejectedValueOnce(new Error('read failed'));
        renderCard();
        const alert = (await screen.findByText(line)).closest('[role="alert"]');
        expect(alert).toHaveClass('text-copper');
        if (heading) expect(screen.getByText(heading)).toBeInTheDocument();
        mocks.getPortrait.mockResolvedValue(portrait());
        await act(async () => { fireEvent.click(screen.getByRole('button', { name: retry ?? i18n.t('memory:aboutYou.retry') })); });
        expect(await screen.findByRole('button', { name: 'plan the whole thing' })).toBeInTheDocument();
        expect(screen.queryByText(line)).not.toBeInTheDocument();
        await i18n.changeLanguage('en');
    });

    // About you v1.0.0 Y15: an answer that failed goes back to the truth, in copper.
    it("That's me that didn't save goes back, with a copper line and Try again", async () => {
        mocks.getPortrait.mockResolvedValue(portrait());
        mocks.confirmPortrait.mockRejectedValueOnce(new Error('offline'));
        renderCard();
        const plan = await screen.findByRole('button', { name: 'plan the whole thing' });
        await act(async () => { fireEvent.click(screen.getByRole('button', { name: "That's me" })); });
        expect(plan).toHaveAttribute('data-underline', 'dotted');
        const alert = screen.getByRole('alert');
        expect(alert).toHaveClass('text-copper');
        expect(screen.getByRole('button', { name: "That's me" })).toBeInTheDocument();
        mocks.confirmPortrait.mockResolvedValueOnce(['plan', 'outline', 'short', 'first']);
        await act(async () => { fireEvent.click(within(alert).getByRole('button', { name: 'Try again' })); });
        expect(mocks.confirmPortrait).toHaveBeenCalledTimes(2);
        expect(screen.getByRole('button', { name: 'plan the whole thing' })).toHaveAttribute('data-underline', 'solid');
    });

    it("That's me answers at once: underlines draw solid, How to work with me follows, the keep line, and Undo puts it back", async () => {
        mocks.getPortrait.mockResolvedValue(portrait());
        mocks.confirmPortrait.mockResolvedValue(['plan', 'outline', 'short', 'first']);
        renderCard();
        const plan = await screen.findByRole('button', { name: 'plan the whole thing' });
        expect(plan).toHaveAttribute('data-underline', 'dotted');
        expect(screen.getByText('From your Slack messages')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: "That's me" }));
        // At once, before the backend answers: each inferred phrase draws in turn.
        expect(plan).toHaveAttribute('data-underline', 'solid');
        expect(plan).toHaveStyle({ '--draw-delay': '120ms' });
        expect(screen.getByRole('button', { name: 'keeps drafts short' })).toHaveStyle({ '--draw-delay': '210ms' });
        // The already confirmed phrase doesn't draw again.
        expect(screen.getByRole('button', { name: 'yeses go out in about 20 minutes' })).not.toHaveAttribute('data-drawing');
        expect(screen.queryByText('From your Slack messages')).not.toBeInTheDocument();
        expect(screen.getAllByText('You confirmed this')).toHaveLength(2);
        await act(async () => {});
        const status = screen.getByRole('status', { name: '' });
        expect(status).toHaveTextContent("You said that's you. The 5 beliefs under it are confirmed.");
        expect(status).toHaveFocus();
        expect(screen.getByTestId('portrait-keeps')).toHaveTextContent('Isoform keeps this version. Anything newer waits for you.');
        expect(screen.queryByRole('button', { name: 'Not quite' })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
        await act(async () => {});
        expect(mocks.undo).toHaveBeenCalledWith(['plan', 'outline', 'short', 'first']);
        expect(screen.getByRole('button', { name: "That's me" })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'plan the whole thing' })).toHaveAttribute('data-underline', 'dotted');
        expect(screen.queryByTestId('portrait-keeps')).toBeNull();
    });

    it("stays answered on a later open, with Not quite; a newer version waits behind Show it", async () => {
        mocks.getPortrait.mockResolvedValue(portrait({ saidAt: Date.now(), pending: true }));
        renderCard();
        expect(await screen.findByText("You said that's you today")).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: "That's me" })).toBeNull();
        expect(screen.getByText('A new version is ready.')).toBeInTheDocument();
        const next = portrait({ generatedAt: now + 3_600_000, name: ['curious', 'connector'] });
        mocks.getPortrait.mockResolvedValue(next);
        fireEvent.click(screen.getByRole('button', { name: 'Show it' }));
        await act(async () => {});
        expect(mocks.showPending).toHaveBeenCalled();
        expect(await screen.findByRole('heading', { name: 'The Curious Connector' })).toBeInTheDocument();
        expect(screen.queryByText('A new version is ready.')).toBeNull();
        expect(screen.getByRole('button', { name: "That's me" })).toBeInTheDocument();
    });

    it('Not quite on a kept version opens the note', async () => {
        mocks.getPortrait.mockResolvedValue(portrait({ saidAt: Date.now() - 86_400_000 }));
        renderCard();
        expect(await screen.findByText("You said that's you yesterday")).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Not quite' }));
        expect(screen.getByLabelText("What's off? You can leave it blank.")).toBeInTheDocument();
    });

    it('Not quite: an optional note, then the old version stays dimmed while it is rewritten', async () => {
        mocks.getPortrait.mockResolvedValue(portrait());
        renderCard();
        fireEvent.click(await screen.findByRole('button', { name: 'Not quite' }));
        const note = screen.getByLabelText("What's off? You can leave it blank.");
        expect(note).toHaveAttribute('placeholder', "For example: I'm not slow on nos, I save them for Friday");
        fireEvent.change(note, { target: { value: 'I save nos for Friday' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await act(async () => {});
        expect(mocks.notQuite).toHaveBeenCalledWith('I save nos for Friday');
        expect(screen.getByText('Rewriting with your note. This version stays until then.')).toBeInTheDocument();
        expect(screen.getByTestId('portrait-paragraph').parentElement).toHaveClass('opacity-55');
        // The rewrite lands at once, with the changed words tinted.
        const rewritten = portrait({ generatedAt: now + 60_000 });
        rewritten.sentences![1] = { ...rewritten.sentences![1], changed: true, parts: [{ text: 'You save the awkward ones for Friday.', memoryIds: ['yes'], confirmed: true }] };
        rewritten.sentences![1].cause = { note: 'I save nos for Friday', notQuite: true };
        mocks.getPortrait.mockResolvedValue(rewritten);
        await emitEvent('portrait:updated');
        expect(await screen.findByTestId('portrait-changed')).toHaveTextContent('You save the awkward ones for Friday.');
        // It says it came from the note, once.
        expect(screen.getByTestId('portrait-why')).toHaveTextContent('Rewritten from your note: “I save nos for Friday”');
        expect(screen.queryByText('A new version is ready.')).not.toBeInTheDocument();
        expect(screen.queryByText('Rewriting with your note. This version stays until then.')).not.toBeInTheDocument();
    });

    it('A new version is ready: nothing changes while someone is reading until Show it', async () => {
        mocks.getPortrait.mockResolvedValue(portrait());
        renderCard();
        expect(await screen.findByRole('button', { name: 'plan the whole thing' })).toBeInTheDocument();
        const next = portrait({ generatedAt: now + 3_600_000, name: ['curious', 'connector'] });
        mocks.getPortrait.mockResolvedValue(next);
        await emitEvent('portrait:updated');
        expect(await screen.findByText('A new version is ready.')).toBeInTheDocument();
        expect(screen.getByRole('heading', { name: 'The Blueprint Builder' })).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Show it' }));
        expect(screen.getByRole('heading', { name: 'The Curious Connector' })).toBeInTheDocument();
        expect(screen.queryByText('A new version is ready.')).not.toBeInTheDocument();
    });

    it('A belief forgotten: only its sentence goes, with the copper note', async () => {
        const forgotten = portrait({ forgotten: true });
        forgotten.sentences = forgotten.sentences!.filter((sentence) => sentence.role !== 'insight');
        mocks.getPortrait.mockResolvedValue(forgotten);
        renderCard();
        expect(await screen.findByText("One sentence rested on what you forgot, so it's gone. Isoform is rewriting the rest around it.")).toHaveClass('text-copper');
        expect(screen.getByTestId('portrait-paragraph')).toHaveTextContent('You plan the whole thing, then build it. So Isoform keeps drafts short.');
    });

    const at = (time: number) => new Intl.DateTimeFormat('en', { hour: 'numeric', minute: '2-digit' }).format(new Date(time));
    const retryAt = now + 15 * 60_000;

    it.each([
        ['limit', `Claude Code reached its limit until ${at(retryAt)}. Isoform writes it then, by itself.`, null],
        ['no_ai', 'Sign in to Claude Code or Codex, and Isoform writes it right away.', 'Sign in'],
        ['no_answer', `Claude Code didn't answer. Isoform tries again at ${at(retryAt)}.`, 'Try now'],
        ['unusable', `Isoform tries again at ${at(retryAt)}.`, 'Try now'],
    ] as const)("Couldn't write (%s): the reason and the time, never a duration, and an action only where it helps", async (reason, copy, action) => {
        mocks.getPortrait.mockResolvedValue(portrait({
            state: 'failed', name: null, sentences: [], workWithMe: [], lately: null,
            failure: { reason, at: now, retryAt, assistant: 'claude-code' },
        }));
        renderCard();
        expect(await screen.findByRole('heading', { name: "Couldn't write it yet" })).toBeInTheDocument();
        expect(screen.getByText(copy)).toBeInTheDocument();
        expect(screen.queryByText(/ in \d+ (minute|hour)/)).toBeNull();
        expect(screen.getByTestId('portrait-shape')).toHaveAttribute('data-shape', 'ring');
        expect(screen.queryByRole('button', { name: 'Try now' }) !== null).toBe(action === 'Try now');
        expect(screen.queryByRole('button', { name: 'Sign in' }) !== null).toBe(action === 'Sign in');
        if (action === 'Try now') {
            fireEvent.click(screen.getByRole('button', { name: 'Try now' }));
            await act(async () => {});
            expect(mocks.retry).toHaveBeenCalled();
            expect(screen.getByRole('heading', { name: 'Writing your portrait' })).toBeInTheDocument();
        }
        if (action === 'Sign in') {
            fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
            expect(mocks.emit).toHaveBeenCalledWith('settings:open', 'ai-provider');
            expect(mocks.retry).not.toHaveBeenCalled();
        }
    });

    it.each([
        ['no_answer', `Codex didn't answer. Isoform tries again at ${at(now + 3_600_000)}.`, true],
        // About you v1.0.0 Y13: a shown portrait that couldn't update says so.
        ['unusable', `Couldn't update it yet. Isoform tries again at ${at(now + 3_600_000)}.`, true],
        ['limit', `Codex reached its limit until ${at(now + 3_600_000)}. Isoform writes it then, by itself.`, false],
    ] as const)('With a portrait shown (%s): it stays under one copper line', async (reason, copy, tryNow) => {
        mocks.getPortrait.mockResolvedValue(portrait({ failure: { reason, at: now, retryAt: now + 3_600_000, assistant: 'codex' } }));
        renderCard();
        const line = await screen.findByText(copy);
        expect(line.parentElement).toHaveClass('text-copper');
        expect(screen.getByRole('heading', { name: 'The Blueprint Builder' })).toBeInTheDocument();
        expect(screen.getByTestId('portrait-paragraph')).toHaveTextContent('You plan the whole thing');
        expect(screen.queryByRole('button', { name: 'Try now' }) !== null).toBe(tryNow);
        if (tryNow) {
            fireEvent.click(screen.getByRole('button', { name: 'Try now' }));
            await act(async () => {});
            expect(mocks.retry).toHaveBeenCalled();
            expect(screen.getByTestId('portrait-paragraph')).toHaveTextContent('You plan the whole thing');
        }
    });

    const rows = [
        { memoryId: 'no', text: 'Sends replies that say no about two days after they arrive', answer: '' as const },
        { memoryId: 'yes', text: 'Says yes within about 20 minutes', answer: '' as const },
        { memoryId: 'move', text: 'Moves morning meetings to the afternoon', answer: '' as const },
    ];

    it('What survived: one sentence shows, with the confirm rows under it', async () => {
        const survived = portrait({ candidates: rows });
        survived.sentences = survived.sentences!.filter((sentence) => sentence.role === 'connect');
        mocks.getPortrait.mockResolvedValue(survived);
        renderCard();
        expect(await screen.findByTestId('portrait-paragraph')).toHaveTextContent('You plan the whole thing, then build it.');
        const box = screen.getByTestId('portrait-confirm-rows');
        expect(within(box).getByText('Confirm these and Isoform can say more')).toBeInTheDocument();
        expect(within(box).getAllByRole('listitem')).toHaveLength(3);
        expect(within(box).getAllByText('Inferred')).toHaveLength(3);
        expect(within(box).getAllByRole('button', { name: "That's me" })).toHaveLength(3);
        expect(within(box).getAllByRole('button', { name: 'Not quite' })).toHaveLength(3);
        expect(within(box).queryByText(/more to unlock|\d+ more/)).toBeNull();
        // The whole-portrait buttons, the legend and the meta stay.
        expect(screen.getByText('You confirmed it')).toBeInTheDocument();
        expect(screen.getByText(/Updated today/)).toBeInTheDocument();
    });

    it("That's me on a row: Confirmed, then the rewrite comes in tinted at once", async () => {
        const survived = portrait({ candidates: rows });
        survived.sentences = survived.sentences!.filter((sentence) => sentence.role === 'connect');
        mocks.getPortrait.mockResolvedValue(survived);
        renderCard();
        const box = await screen.findByTestId('portrait-confirm-rows');
        const row = within(box).getByText('Says yes within about 20 minutes').closest('li')!;
        fireEvent.click(within(row).getByRole('button', { name: "That's me" }));
        await act(async () => {});
        expect(mocks.answer).toHaveBeenCalledWith('yes', true);
        expect(row).toHaveAttribute('data-answer', 'confirmed');
        expect(within(row).getByText('Confirmed')).toBeInTheDocument();
        expect(within(row).queryByText('Inferred')).toBeNull();
        expect(screen.getByText('Writing with what you confirmed')).toBeInTheDocument();
        // The sentence comes back: it applies without "Show it", tinted, and says why.
        const rewritten = portrait({ generatedAt: now + 40_000, candidates: [] });
        rewritten.sentences = rewritten.sentences!.map((sentence) => ({ ...sentence, changed: sentence.role === 'insight', cause: sentence.role === 'insight' ? { confirm: ['yes'], note: '', notQuite: false } : null }));
        mocks.getPortrait.mockResolvedValue(rewritten);
        await emitEvent('portrait:updated');
        expect(screen.queryByRole('button', { name: 'Show it' })).toBeNull();
        expect(screen.getByTestId('portrait-changed')).toHaveTextContent('Your yeses go out in about 20 minutes.');
        expect(screen.getByTestId('portrait-why')).toHaveTextContent('Because you confirmed “Says yes within about 20 minutes”.');
        // The row stays, answered, until the person leaves.
        const kept = within(screen.getByTestId('portrait-confirm-rows')).getByText('Says yes within about 20 minutes').closest('li')!;
        expect(kept).toHaveAttribute('data-answer', 'confirmed');
        fireEvent.click(within(kept).getByRole('button', { name: 'Undo' }));
        await act(async () => {});
        expect(mocks.undoRow).toHaveBeenCalledWith('yes', true);
    });

    it('rows answered on an earlier visit are gone on the next open', async () => {
        const survived = portrait({ candidates: [{ ...rows[0], answer: 'confirmed' }, rows[1]] });
        mocks.getPortrait.mockResolvedValue(survived);
        renderCard();
        const box = await screen.findByTestId('portrait-confirm-rows');
        expect(within(box).getAllByRole('listitem')).toHaveLength(1);
        expect(within(box).queryByText(rows[0].text)).toBeNull();
    });

    it("Not quite on a row: the row reads Won't use it, with Undo", async () => {
        const survived = portrait({ candidates: rows });
        survived.sentences = survived.sentences!.filter((sentence) => sentence.role === 'connect');
        mocks.getPortrait.mockResolvedValue(survived);
        renderCard();
        const box = await screen.findByTestId('portrait-confirm-rows');
        const row = within(box).getByText('Moves morning meetings to the afternoon').closest('li')!;
        fireEvent.click(within(row).getByRole('button', { name: 'Not quite' }));
        await act(async () => {});
        expect(mocks.answer).toHaveBeenCalledWith('move', false);
        expect(row).toHaveAttribute('data-answer', 'noted');
        expect(within(row).getByText("Won't use it")).toBeInTheDocument();
        expect(within(row).queryByText('Noted')).toBeNull();
        expect(screen.queryByText('Writing with what you confirmed')).toBeNull();
        fireEvent.click(within(row).getByRole('button', { name: 'Undo' }));
        await act(async () => {});
        expect(mocks.undoRow).toHaveBeenCalledWith('move', false);
        expect(within(row).getByRole('button', { name: "That's me" })).toBeInTheDocument();
    });

    it('Only the shape: the name, the shape, one line and the rows; no paragraph, legend or whole-portrait buttons', async () => {
        mocks.getPortrait.mockResolvedValue(portrait({ state: 'shape', sentences: [], lately: null, candidates: rows }));
        renderCard();
        expect(await screen.findByRole('heading', { name: 'The Blueprint Builder' })).toBeInTheDocument();
        expect(screen.getByTestId('portrait-shape')).toHaveAttribute('data-shape', 'figure');
        expect(screen.getByRole('button', { name: 'Why this name' })).toBeInTheDocument();
        expect(screen.getByTestId('portrait-shape-line')).toHaveTextContent('Isoform has your shape. To put it into words, it needs a little more to go on: confirm the ones below, or give it a few more days.');
        expect(within(screen.getByTestId('portrait-confirm-rows')).getAllByRole('listitem')).toHaveLength(3);
        expect(screen.queryByTestId('portrait-paragraph')).toBeNull();
        expect(screen.queryByText('You confirmed it')).toBeNull();
        expect(screen.queryByText(/Updated today/)).toBeNull();
        expect(screen.queryByText("Couldn't write it yet")).toBeNull();
        expect(screen.queryByRole('button', { name: 'Try now' })).toBeNull();
        // How to work with me shows its lines that passed.
        expect(screen.getByRole('heading', { name: 'How to work with me' })).toBeInTheDocument();
    });

    it('shows the v2.0.0 copy exactly in zh-CN', async () => {
        await i18n.changeLanguage('zh-CN');
        mocks.getPortrait.mockResolvedValue(portrait({ state: 'shape', sentences: [], lately: null, candidates: rows.slice(0, 1) }));
        renderCard();
        expect(await screen.findByText('Isoform 已经勾勒出你的轮廓。要把它写成文字，还需要多一点依据：确认下面几条，或者再给它几天。')).toBeInTheDocument();
        expect(screen.getByText('确认这几条，Isoform 就能写得更多')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: '说得对' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: '不太对' })).toBeInTheDocument();
    });

    // About you v1.0.0 Y23: a copy that failed keeps Copy and says so in copper.
    it.each([
        ['en', 'Copy', "Couldn't copy.", 'Try again'],
        ['zh-CN', null, '没能复制。', null],
    ] as const)('Copy that failed stays Copy with a copper line in %s', async (language, copyName, line, retry) => {
        await i18n.changeLanguage(language);
        mocks.getPortrait.mockResolvedValue(portrait());
        mocks.copy.mockResolvedValueOnce(false);
        renderCard();
        const label = copyName ?? i18n.t('memory:aboutYou.portrait.workWithMe.copy');
        const copy = await screen.findByRole('button', { name: label });
        await act(async () => { fireEvent.click(copy); });
        expect(screen.getByRole('button', { name: label })).toBeInTheDocument();
        expect(screen.getByText(line).closest('[role="alert"]')).toHaveClass('text-copper');
        mocks.copy.mockResolvedValueOnce(true);
        await act(async () => { fireEvent.click(screen.getByRole('button', { name: retry ?? i18n.t('memory:aboutYou.retry') })); });
        expect(mocks.copy).toHaveBeenCalledTimes(2);
        expect(screen.queryByText(line)).not.toBeInTheDocument();
        await i18n.changeLanguage('en');
    });

    it('Copy puts the title and lines on the clipboard and reads Copied for 1.6 s', async () => {
        mocks.getPortrait.mockResolvedValue(portrait());
        renderCard();
        const copy = await screen.findByRole('button', { name: 'Copy' });
        vi.useFakeTimers();
        await act(async () => { fireEvent.click(copy); });
        expect(mocks.copy).toHaveBeenCalledWith("How to work with me\n\n- Send the number, not the opinion.\n- Short replies aren't cold.");
        expect(screen.getByRole('button', { name: 'Copied' })).toBeInTheDocument();
        act(() => { vi.advanceTimersByTime(1600); });
        expect(screen.getByRole('button', { name: 'Copy' })).toBeInTheDocument();
    });

    it('marks changed words seen when the card goes away', async () => {
        const changed = portrait();
        changed.sentences![0] = { ...changed.sentences![0], changed: true };
        mocks.getPortrait.mockResolvedValue(changed);
        const { unmount } = renderCard();
        expect(await screen.findByTestId('portrait-changed')).toBeInTheDocument();
        unmount();
        expect(mocks.seen).toHaveBeenCalled();
    });

    it('names the person exactly in zh-CN', async () => {
        await i18n.changeLanguage('zh-CN');
        mocks.getPortrait.mockResolvedValue(portrait());
        renderCard();
        expect(await screen.findByRole('heading', { name: '蓝图型建造者' })).toBeInTheDocument();
        expect(screen.getByText('说得对')).toBeInTheDocument();
        expect(screen.getByText('今天更新')).toBeInTheDocument();
    });
});
