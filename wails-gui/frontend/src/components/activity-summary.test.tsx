import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { AISummaryCard } from './activity-summary';

// AR63: "This day isn't written yet" appears only after the read has answered.
const base = {
    summary: '',
    summaryStatus: 'none',
    generating: false,
    onGenerate: vi.fn(),
    emptyActionLabel: 'Write it',
    emptyText: "This day isn't written yet.",
};

describe('AISummaryCard while the day is loading', () => {
    beforeEach(async () => {
        await i18n.changeLanguage('en');
    });

    it('holds the layout with no sentence and no button until the read answers', () => {
        const { container } = render(<AISummaryCard {...base} pending />);
        expect(screen.queryByText("This day isn't written yet.")).toBeNull();
        expect(screen.queryByRole('button', { name: 'Write it' })).toBeNull();
        expect(container.querySelector('.animate-pulse')).not.toBeNull();
    });

    it('shows the empty state once the read has answered with no summary', () => {
        render(<AISummaryCard {...base} pending={false} />);
        expect(screen.getByText("This day isn't written yet.")).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Write it' })).toBeInTheDocument();
    });

    it('paints a saved summary at once, without the empty sentence', () => {
        render(<AISummaryCard {...base} summary="Shipped the relay fix." summaryStatus="ready" pending={false} />);
        expect(screen.getByText('Shipped the relay fix.')).toBeInTheDocument();
        expect(screen.queryByText("This day isn't written yet.")).toBeNull();
    });
});
