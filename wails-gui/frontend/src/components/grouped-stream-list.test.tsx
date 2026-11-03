import { cleanup, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { StreamItem } from '@/types';
import { GroupedStreamList } from './grouped-stream-list';

vi.mock('./stream-card', () => ({ StreamCard: ({ item }: { item: StreamItem }) => <div>{item.content}</div> }));
vi.mock('./processing-block', () => ({ ProcessingBlock: () => <div /> }));

beforeEach(async () => {
    await i18n.changeLanguage('en');
});
afterEach(cleanup);

describe('conversation', () => {
    // The user: "we dont need to mention answered by whom". Who answered, why a
    // pick was skipped and Try again live in Settings > Assistant only.
    it('shows no attribution: no runner, model, effort or skipped line', () => {
        render(<GroupedStreamList streaming={false} items={[
            { id: 'u1', kind: 'user', content: 'First request' },
            { id: 'failed', kind: 'error', content: 'Retrying', runId: 'assistant-attempt' },
            { id: 'a1', kind: 'assistant', content: 'Completed', runId: 'native-backup' },
            { id: 'u2', kind: 'user', content: 'Background task' },
            { id: 'a2', kind: 'assistant', content: 'Background result', runId: 'background-run' },
        ]} />);
        expect(screen.getByText('Completed')).toBeInTheDocument();
        expect(screen.queryByText(/Answered by|Answering with|Ran on|Running on|Claude Code|Codex|Isoform/)).not.toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Settings' })).not.toBeInTheDocument();
    });

    it('marks each prompt and answer with its item id, so Search can scroll to a message', () => {
        render(<GroupedStreamList streaming={false} items={[
            { id: 'u1', kind: 'user', content: 'First request' },
            { id: 'a1', kind: 'assistant', content: 'First answer', runId: 'r1' },
        ]} />);
        const prompt = document.querySelector('[data-stream-item-id="u1"]');
        const answer = document.querySelector('[data-stream-item-id="a1"]');
        expect(prompt).toHaveTextContent('First request');
        expect(answer).toHaveTextContent('First answer');
        // The wrappers add no box: the cards stay the turn's flex items.
        expect(prompt).toHaveClass('contents');
        expect(answer).toHaveClass('contents');
    });

    it.each(['context_status', 'system'] as const)('keeps a late %s Today receipt with its turn', (kind) => {
        render(<GroupedStreamList streaming={false} items={[
            { id: 'today-user', kind: 'user', content: 'Discuss Today' },
            { id: 'today-answer', kind: 'assistant', content: 'Today answer', runId: 'native-backup' },
            { id: 'next-user', kind: 'user', content: 'Next request' },
            { id: 'next-answer', kind: 'assistant', content: 'Next answer', runId: 'next-run' },
            { id: 'today-receipt', kind, toolName: 'today_context', content: 'Today context receipt', toolInput: { userItemId: 'today-user', memories: 1, activities: 2, people: 0 } },
        ]} />);
        const todayTurn = within(screen.getByText('Discuss Today').closest<HTMLElement>('[data-stream-turn]')!);
        expect(todayTurn.getByText('Today context receipt')).toBeInTheDocument();
        const nextTurn = within(screen.getByText('Next request').closest<HTMLElement>('[data-stream-turn]')!);
        expect(nextTurn.queryByText('Today context receipt')).not.toBeInTheDocument();
    });
});
