import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { resetPageQueries } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { ActivityCaptureLine } from './activity-capture-line';

const mocks = vi.hoisted(() => ({ read: vi.fn() }));
vi.mock('@/lib/wails-shim-app', () => ({ GetCaptureCounts: mocks.read }));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => undefined }));

const counts = {
    paused: false, notReady: false, todayStart: 0, weekStart: 0,
    sources: [{ source: 'screens', today: 1483, week: 9000 }, { source: 'audioMinutes', today: 239, week: 900 }],
};
const midnight = () => { const now = new Date(); return new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime(); };

beforeEach(async () => {
    await i18n.changeLanguage('en');
    resetPageQueries();
    mocks.read.mockReset().mockResolvedValue(counts);
});
afterEach(() => resetPageQueries());

// Snappy 1.0 (section 4): the line moved from Today to Activity and is read
// after the page paints, never on the click that opened it.
it('reads the counts only after the page has painted, then says what Isoform captured today', async () => {
    render(<ActivityCaptureLine />);
    expect(mocks.read).not.toHaveBeenCalled();
    expect(screen.queryByTestId('activity-capture')).not.toBeInTheDocument();
    await act(async () => { await new Promise((resolve) => requestAnimationFrame(() => resolve(undefined))); });
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(mocks.read).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('activity-capture')).toHaveTextContent('Today Isoform saw 1,483 screens and heard speech during 239 minutes.');
});

it('shows a cached answer at once', () => {
    pageQueryCache.set('GetCaptureCounts', [midnight()], counts);
    render(<ActivityCaptureLine />);
    expect(screen.getByTestId('activity-capture')).toHaveTextContent('1,483 screens');
});

it('says Chinese words in Chinese', async () => {
    await i18n.changeLanguage('zh-CN');
    pageQueryCache.set('GetCaptureCounts', [midnight()], counts);
    render(<ActivityCaptureLine />);
    expect(screen.getByTestId('activity-capture').textContent).toMatch(/[㐀-鿿]/);
});
