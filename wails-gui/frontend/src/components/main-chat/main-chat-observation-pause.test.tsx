import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { computerClock } from '@/lib/brief-time';
import { MainChatObservationPause } from './main-chat-observation-pause';

const resume = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
vi.mock('@/lib/wails-shim-app', () => ({ ResumeObservation: resume }));
afterEach(() => { vi.clearAllMocks(); });
describe('observation pause', () => {
    it.each(['en', 'zh-CN'])('replaces the active card with one sourced quiet line in %s', async (language) => {
        await i18n.changeLanguage(language);
        const start = new Date(2026, 10, 1, 9, 30).getTime();
        const end = new Date(2026, 10, 1, 10, 15).getTime();
        const { rerender, unmount } = render(<MainChatObservationPause input={{ startAt: start }} />);
        expect(screen.getByText(i18n.t('chat:mainChat.observationPause.paused', { time: computerClock(new Date(start), language) }))).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: i18n.t('chat:mainChat.observationPause.resume') }));
        await waitFor(() => expect(resume).toHaveBeenCalledOnce());
        rerender(<MainChatObservationPause input={{ startAt: start, endAt: end }} />);
        expect(screen.queryByRole('button')).not.toBeInTheDocument();
        const range = i18n.t('chat:mainChat.observationPause.pausedFromTo', { start: computerClock(new Date(start), language), end: computerClock(new Date(end), language) });
        expect(screen.getByText(new RegExp(language === 'en' ? 'Observing paused from' : '观察于'))).toHaveTextContent(`${range} · ${i18n.t('chat:mainChat.proactive.sourceFrom', { source: i18n.t('chat:mainChat.proactive.source.observing') })}`);
        if (language === 'en') expect(range).toBe('Observing paused from 9:30 AM to 10:15 AM.');
        unmount();
    });
    it('does not invent a recovery time or show Resume after an unrecorded recovery', async () => {
        await i18n.changeLanguage('en');
        const start = Date.now() - 60_000;
        const { unmount } = render(<MainChatObservationPause input={{ startAt: start, resumedUnknown: true }} />);
        const line = screen.getByText(/Observing paused at/);
        expect(line).toHaveTextContent(new RegExp(`^Observing paused at ${computerClock(new Date(start), 'en')}\\. · From Observing$`));
        expect(line.textContent).not.toMatch(/on again/);
        expect(line).toHaveTextContent('From Observing');
        expect(screen.queryByText(/Observing again at/)).toBeNull();
        expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull();
        unmount();
    });
});
