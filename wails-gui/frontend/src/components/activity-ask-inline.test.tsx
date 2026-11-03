import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { ActivityAskInline, ActivityOpenChatContext } from './activity-ask-inline';

const chat = vi.hoisted(() => ({
    send: vi.fn(),
    items: [] as { id: string; kind: string; content?: string }[],
    lastUserItemId: '',
    pending: null as null | { state: string },
    waiting: false,
}));
vi.mock('@/hooks/useMainChatPanel', () => ({ useMainChatPanel: () => chat }));

const ref = { sourceKind: 'activity', sourceId: 'a1', dayStartMs: 1 };

describe('ActivityAskInline', () => {
    beforeEach(async () => {
        await i18n.changeLanguage('en');
        chat.send.mockReset().mockResolvedValue('sent');
        chat.items = [];
        chat.lastUserItemId = '';
        chat.pending = null;
        chat.waiting = false;
    });

    it('focuses the field and asks the main chat with the item as context', async () => {
        render(<ActivityAskInline context={ref} onClose={vi.fn()} />);
        const field = screen.getByRole('textbox', { name: 'Ask anything about this' });
        expect(field).toHaveFocus();
        fireEvent.change(field, { target: { value: 'What was left?' } });
        fireEvent.click(screen.getByRole('button', { name: 'Ask' }));
        expect(chat.send).toHaveBeenCalledWith('What was left?', ref, true);
    });

    it('shows the answer under the field with Open in Chat', async () => {
        const openChat = vi.fn();
        const view = render(
            <ActivityOpenChatContext.Provider value={openChat}>
                <ActivityAskInline context={ref} onClose={vi.fn()} />
            </ActivityOpenChatContext.Provider>,
        );
        fireEvent.change(screen.getByRole('textbox', { name: 'Ask anything about this' }), { target: { value: 'What was left?' } });
        fireEvent.click(screen.getByRole('button', { name: 'Ask' }));
        chat.lastUserItemId = 'u1';
        chat.items = [{ id: 'u1', kind: 'user', content: 'What was left?' }, { id: 'r1', kind: 'assistant', content: 'The CI fix.' }];
        view.rerender(
            <ActivityOpenChatContext.Provider value={openChat}>
                <ActivityAskInline context={ref} onClose={vi.fn()} />
            </ActivityOpenChatContext.Provider>,
        );
        expect(await screen.findByText('The CI fix.')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Open in Chat' }));
        expect(openChat).toHaveBeenCalled();
    });

    it('says in place when the question could not be sent, with Try again', async () => {
        chat.send.mockResolvedValueOnce('failed').mockResolvedValueOnce('sent');
        render(<ActivityAskInline context={ref} onClose={vi.fn()} />);
        fireEvent.change(screen.getByRole('textbox', { name: 'Ask anything about this' }), { target: { value: 'Why?' } });
        fireEvent.click(screen.getByRole('button', { name: 'Ask' }));
        expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't send your question.");
        fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
        expect(chat.send).toHaveBeenLastCalledWith('Why?', ref, true);
    });
});
