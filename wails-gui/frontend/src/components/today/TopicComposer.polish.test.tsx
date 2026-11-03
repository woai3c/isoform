import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { TopicComposer } from './TopicComposer';

// Layout/component guard only: synthetic labels, no backend integration.
describe('Topic composer bottom row', () => {
    it('keeps the text line free and detaches the topic from the bottom toolbar without sending', () => {
        const send = vi.fn();
        const remove = vi.fn();
        render(<TopicComposer draft="" prepared={false} onDraftChange={vi.fn()} onSend={send} sending={false}
            inputLabel="Message" placeholder="Message Isoform" sendLabel="Send" note="Topic attached"
            chip={{ name: 'Halcyon ERP sync', removeLabel: 'Remove topic', onRemove: remove }} />);
        const text = screen.getByRole('textbox');
        expect(text.parentElement?.querySelector('button, svg')).toBeNull();
        const toolbar = screen.getByTestId('topic-composer-toolbar');
        expect(within(toolbar).getByText('Halcyon ERP sync')).toBeInTheDocument();
        expect(within(toolbar).getByRole('button', { name: 'Send' })).toBeInTheDocument();
        fireEvent.click(within(toolbar).getByRole('button', { name: 'Remove topic' }));
        expect(remove).toHaveBeenCalledOnce();
        expect(send).not.toHaveBeenCalled();
    });
});

it.each(['paper', 'onedark'])('uses the primary fill on a topic Send, keeping its surface, empty Send and Stop flat in %s', (theme) => {
    document.documentElement.dataset.theme = theme;
    const props = { draft: 'A question', prepared: false, onDraftChange: vi.fn(), onSend: vi.fn(), sending: false,
        inputLabel: 'Message', placeholder: 'Message', sendLabel: 'Send', note: '' };
    const { rerender, container } = render(<TopicComposer {...props} />);
    expect(screen.getByRole('button', { name: 'Send' })).toHaveClass('patina-verdigris');
    expect(container.querySelector('[data-topic-composer-surface]')).not.toHaveClass('patina-ground', 'patina-verdigris');
    rerender(<TopicComposer {...props} draft="" />);
    expect(screen.getByRole('button', { name: 'Send' })).not.toHaveClass('patina-verdigris');
    rerender(<TopicComposer {...props} stop={{ label: 'Stop', onStop: vi.fn() }} />);
    expect(screen.getByRole('button', { name: 'Stop' })).not.toHaveClass('patina-verdigris');
});
