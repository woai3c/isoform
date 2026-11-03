import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { getMainChatProactive, MAIN_CHAT_PROACTIVE_TOOL } from '@/lib/stream-helpers';
import { FirstReadCard } from './first-read-card';

const answer = vi.hoisted(() => vi.fn());
vi.mock('@/lib/wails-shim-app', () => ({ AnswerFirstRead: answer }));

const item = { kind: 'system', toolName: MAIN_CHAT_PROACTIVE_TOOL, toolInput: {
    momentId: 'first-read:1', kind: 'first_read', headline: 'What I see so far', body: 'You build things.',
    sentences: ['You build things.'], helps: ['Help Sam.', 'Brief at 7:30.'], offerCount: 2,
    actionIds: [], actionLabels: {}, state: 'pending', target: { kind: 'none' }, evidence: [],
} } as const;

beforeEach(async () => { await i18n.changeLanguage('en'); answer.mockReset(); });
afterEach(cleanup);

describe('first read', () => {
    it('preserves the projected fields and confirms, then undoes', async () => {
        const message = getMainChatProactive(item)!;
        expect(message.sentences).toEqual(['You build things.']);
        expect(message.helps).toEqual(['Help Sam.', 'Brief at 7:30.']);
        expect(message.offerCount).toBe(2);
        answer.mockResolvedValueOnce({ state: 'acted', confirmedAt: Date.now() }).mockResolvedValueOnce({ state: 'pending' });
        const open = vi.fn();
        render(<FirstReadCard message={message} onOpenTarget={open} />);
        fireEvent.click(screen.getByRole('button', { name: "That's me" }));
        expect(screen.getByTestId('first-read')).toHaveTextContent("You said that's you");
        await waitFor(() => expect(answer).toHaveBeenCalledWith('first-read:1', 'confirm_portrait', ''));
        fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
        await waitFor(() => expect(screen.getByRole('button', { name: "That's me" })).toBeInTheDocument());
        expect(answer).toHaveBeenCalledWith('first-read:1', 'undo_portrait', '');
        fireEvent.click(screen.getByRole('button', { name: 'See them' }));
        expect(open).toHaveBeenCalledWith({ kind: 'tasks' });
    });
    it('sends a correction and hides the answered card', async () => {
        answer.mockResolvedValue({ state: 'corrected' });
        render(<FirstReadCard message={getMainChatProactive(item)!} />);
        fireEvent.click(screen.getByRole('button', { name: 'Not quite' }));
        fireEvent.change(screen.getByLabelText("What's off?"), { target: { value: 'I work later.' } });
        fireEvent.click(screen.getByRole('button', { name: 'Send' }));
        await waitFor(() => expect(screen.queryByTestId('first-read')).not.toBeInTheDocument());
        expect(answer).toHaveBeenCalledWith('first-read:1', 'correct_portrait', 'I work later.');
    });
    it('retains confirmation and shows an error if a queued Undo fails', async () => {
        let finish!: (result: { state: string; confirmedAt: number }) => void;
        answer.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }))
            .mockRejectedValueOnce(new Error('unavailable'));
        render(<FirstReadCard message={getMainChatProactive(item)!} />);
        fireEvent.click(screen.getByRole('button', { name: "That's me" }));
        fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
        await act(async () => { finish({ state: 'acted', confirmedAt: Date.now() }); });
        await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
        expect(screen.getByTestId('first-read')).toHaveTextContent("You said that's you");
    });
});
