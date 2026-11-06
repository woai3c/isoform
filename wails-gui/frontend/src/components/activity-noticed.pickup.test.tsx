import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { ActivityNoticed } from './activity-noticed';
import type { PickUpNotice } from './activity-stitched';

vi.mock('@/lib/page-queries', () => ({
    GetDayNoticed: vi.fn(async () => []),
    usePageQueryRefresh: vi.fn(),
}));

// AR62: a redaction tag never shows in Isoform noticed, including a title saved before the fix.
const notice = (title: string): PickUpNotice => ({
    key: 'k', threadId: 'th', title, steppedAwayMs: Date.UTC(2026, 10, 5, 20, 0), fromMs: Date.UTC(2026, 10, 5, 19, 0),
    label: '', momentId: '', momentTitle: '', momentStartMs: 0, activeMs: 47 * 60_000,
});

describe('Isoform noticed pick-up title', () => {
    beforeEach(async () => {
        await i18n.changeLanguage('en');
    });

    it('shows a title with the tag written around, never the tag itself', async () => {
        const { container } = render(<ActivityNoticed dateStr="2026-11-05" onOpenMoment={vi.fn()} pickUp={notice('Isoform release')} />);
        expect(await screen.findByText('Pick up where you left off: Isoform release.')).toBeInTheDocument();
        expect(container.textContent).not.toMatch(/\[REDACTED|REDACTED/);
    });

    it('says a private item when a saved title is only a tag', async () => {
        const { container } = render(<ActivityNoticed dateStr="2026-11-05" onOpenMoment={vi.fn()} pickUp={notice('[REDACTED:code]')} />);
        await waitFor(() => expect(screen.getByText('Pick up where you left off: a private item.')).toBeInTheDocument());
        expect(container.textContent).not.toMatch(/REDACTED/);
    });
});
