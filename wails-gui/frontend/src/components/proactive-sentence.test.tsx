import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { MainChatProactiveCard } from './main-chat-proactive-card';
import { MAIN_CHAT_PROACTIVE_TOOL } from '@/lib/stream-helpers';
import type { StreamItem } from '@/types';

const getGoal = vi.hoisted(() => vi.fn());
vi.mock('@/lib/wails-shim-app', () => ({
    IdeaMeaningGroups: () => Promise.resolve(null),
    GetGoal: getGoal, RecordMainChatProactiveReceipt: vi.fn(), ResolveMainChatProactiveAction: vi.fn(),
}));
vi.mock('@/lib/wails-shim-runtime', () => ({ EventsOn: () => () => {}, ClipboardSetText: vi.fn() }));
const sourceId = '11111111-1111-4111-8111-111111111111';
const item = (body = '', id = sourceId): StreamItem => ({
    id, kind: 'system', toolName: MAIN_CHAT_PROACTIVE_TOOL,
    toolInput: { momentId: id, kind: 'commitment_slipping', headline: 'A commitment needs attention.', body,
        draft: false, actionIds: ['open'], actionLabels: { open: 'Open' }, state: 'pending', target: { kind: 'today' },
        evidence: [{ kind: 'commitment', id, target: { kind: 'today' } }] },
});

beforeEach(async () => {
    await i18n.changeLanguage('en');
    getGoal.mockReset().mockResolvedValue({ goal: { id: sourceId, title: 'Countersign the Northwind SOW', dueAt: new Date(2026, 9, 23, 12).toISOString() } });
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 9, 23, 9));
});
afterEach(() => vi.useRealTimers());

describe.each(['panel', 'stream'])('Specific proactive sentence in the %s', (surface) => {
    function mount(card: StreamItem) {
        return render(surface === 'panel' ? <MainChatProactiveCard item={card} compact panelSurface="today" /> : <MainChatProactiveCard item={card} />);
    }
    it.each(['protected source', 'Closed Eyes', 'missing provenance'])('keeps withdrawn %s copy generic without a goal read', async () => {
        // Delivery intentionally retains the commitment evidence ID after
        // replacing every localized display field with source-free catalog copy.
        const withdrawn = item('A commitment needs attention.');
        mount(withdrawn);
        await Promise.resolve();
        expect(getGoal).not.toHaveBeenCalled();
        expect(screen.queryByText(/Countersign the Northwind SOW/)).not.toBeInTheDocument();
        expect(screen.getByText('A commitment needs attention.')).toBeInTheDocument();
    });
    it('removes an already rendered sentence when delivery withdraws the source', () => {
        const visible = item('Countersign the Northwind SOW · due today 12:00 PM');
        const view = mount(visible);
        expect(screen.getByText(/Countersign the Northwind SOW/)).toBeInTheDocument();
        const withdrawn = item('A commitment needs attention.');
        view.rerender(surface === 'panel' ? <MainChatProactiveCard item={withdrawn} compact panelSurface="today" /> : <MainChatProactiveCard item={withdrawn} />);
        expect(screen.queryByText(/Countersign the Northwind SOW/)).not.toBeInTheDocument();
        expect(getGoal).not.toHaveBeenCalled();
    });
    it('keeps a supplied specific sentence without reading another source', async () => {
        mount(item('Northwind is waiting for your countersignature.'));
        expect(screen.getByText('Northwind is waiting for your countersignature.')).toBeInTheDocument();
        expect(getGoal).not.toHaveBeenCalled();
    });
    it('renders the backend-projected sentence in Chinese without resolving evidence', async () => {
        await i18n.changeLanguage('zh-CN');
        mount(item('Countersign the Northwind SOW · 今天中午12:00到期'));
        expect(screen.getByText('Countersign the Northwind SOW · 今天中午12:00到期')).toBeInTheDocument();
        expect(getGoal).not.toHaveBeenCalled();
    });
});


describe.each(['panel', 'stream'])('Polish 6 sentence projection in %s', (surface) => {
    function mount(card: StreamItem) {
        return render(surface === 'panel' ? <MainChatProactiveCard item={card} compact panelSurface="today" /> : <MainChatProactiveCard item={card} />);
    }
    it.each(['en', 'zh-CN'])('uses the delivered body when a localized catalog entry has no sentence in %s', async (language) => {
        await i18n.changeLanguage(language);
        const sentence = language === 'en' ? 'Countersign the Northwind SOW · due today 12:00 PM' : 'Countersign the Northwind SOW · 今天中午12:00到期';
        const card = item(sentence);
        (card.toolInput as Record<string, unknown>).localized = { [language]: { headline: 'A commitment needs attention.', body: 'A commitment needs attention.', actionLabels: { open: 'Open' } } };
        mount(card);
        expect(screen.getByText(sentence)).toBeInTheDocument();
        expect(getGoal).not.toHaveBeenCalled();
    });
    it.each(['en', 'zh-CN'])('does not invent a sentence when delivery withholds the source in %s', async (language) => {
        await i18n.changeLanguage(language);
        mount(item('A commitment needs attention.'));
        expect(screen.queryByText(/details unavailable|sentence unavailable/i)).not.toBeInTheDocument();
        if (surface === 'panel') {
            expect(screen.queryByTestId('main-chat-panel-card-line')).not.toBeInTheDocument();
        }
        expect(getGoal).not.toHaveBeenCalled();
    });
});
