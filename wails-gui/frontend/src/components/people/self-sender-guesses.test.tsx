import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';

const mocks = vi.hoisted(() => ({ merge: vi.fn(), notSame: vi.fn() }));
vi.mock('@/lib/wails-shim-app', () => ({ MergePeople: mocks.merge, NotSamePeople: mocks.notSame }));
import { SelfSenderGuesses } from './self-sender-guesses';

beforeEach(async () => { vi.clearAllMocks(); await i18n.changeLanguage('en'); });

it('asks before merging a possible sender and saves a rejection', async () => {
    const onMerged = vi.fn().mockResolvedValue(undefined);
    const onNotSame = vi.fn().mockResolvedValue(undefined);
    mocks.notSame.mockResolvedValue(undefined);
    const guess = { selfId: 'self', personId: 'sender', name: 'Bo', app: 'slack' };
    render(<SelfSenderGuesses guesses={[guess]} onMerged={onMerged} onNotSame={onNotSame} />);
    expect(screen.getByText('Is this you? Bo in Slack')).toBeInTheDocument();
    expect(mocks.merge).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Not me' }));
    await waitFor(() => expect(mocks.notSame).toHaveBeenCalledWith(['self', 'sender']));
    expect(onNotSame).toHaveBeenCalledWith(['self', 'sender']);
});

it('merges only after explicit confirmation', async () => {
    const onMerged = vi.fn().mockResolvedValue(undefined);
    mocks.merge.mockResolvedValue({ mergeId: 'receipt', target: { name: 'Bo' } });
    render(<SelfSenderGuesses guesses={[{ selfId: 'self', personId: 'sender', name: 'Bo', app: 'slack' }]} onMerged={onMerged} onNotSame={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'This is me' }));
    await waitFor(() => expect(mocks.merge).toHaveBeenCalledWith('self', ['sender']));
    expect(onMerged).toHaveBeenCalled();
});
