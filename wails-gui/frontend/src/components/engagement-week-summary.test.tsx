import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import en from '@/i18n/locales/en/settings.json';
import zh from '@/i18n/locales/zh-CN/settings.json';
import { EngagementWeekLine, EngagementWeekSummary, useEngagementWeek } from './engagement-week-summary';

// The Notifications page reads the week once and shows its line and its table.
function Week({ table = true }: { table?: boolean }) {
    const week = useEngagementWeek();
    return <>
        <EngagementWeekLine week={week} />
        {table && <EngagementWeekSummary week={week} />}
    </>;
}

const getWeek = vi.hoisted(() => vi.fn());
vi.mock('@/lib/wails-shim-app', () => ({ GetEngagementWeekSummary: getWeek }));

const fixture = {
    available: true, days: 7, sent: 10, actedOn: 3,
    kinds: [
        { kind: 'brief_ready', created: 12, scheduled: 11, sent: 10, delivered: 9, opened: 8, acted: 3, dismissed: 2, muted: 1, actRate: 0.3 },
        { kind: 'meeting_prep', created: 1, scheduled: 0, sent: 0, delivered: 0, opened: 0, acted: 0, dismissed: 0, muted: 0, actRate: 0 },
    ],
};

describe('weekly Engagement in Notifications', () => {
    beforeEach(async () => {
        vi.resetAllMocks();
        await i18n.changeLanguage('en');
        getWeek.mockResolvedValue(fixture);
    });

    it('shows all receipt counts per kind and the completed-to-sent act rate', async () => {
        render(<Week />);
        const table = await screen.findByRole('table', { name: 'Engagement' });
        const row = within(table).getByRole('rowheader', { name: 'Brief ready' }).closest('tr')!;
        expect(within(row).getAllByRole('cell').map((cell) => cell.textContent)).toEqual(['12', '11', '10', '9', '8', '3', '2', '1', '30%']);
        const unsent = within(table).getByRole('rowheader', { name: 'Meeting coming up' }).closest('tr')!;
        expect(within(unsent).getByText('No sends')).toBeInTheDocument();
        expect(screen.getByText(en.proactiveNotifications.engagement.measurement)).toBeInTheDocument();
        expect(screen.getByText(en.proactiveNotifications.engagement.description)).toBeInTheDocument();
    });

    it('refreshes when the window regains focus and shows an empty week honestly', async () => {
        render(<Week />);
        await screen.findByRole('table');
        getWeek.mockResolvedValue({ ...fixture, sent: 0, actedOn: 0, kinds: [] });
        await act(async () => window.dispatchEvent(new Event('focus')));
        expect(screen.getByText(en.proactiveNotifications.engagement.empty)).toBeInTheDocument();
        expect(getWeek).toHaveBeenCalledTimes(2);
    });

    it('offers a retry after a failed read and does not show stale counts', async () => {
        getWeek.mockRejectedValueOnce(new Error('unavailable'));
        render(<Week />);
        const alerts = await screen.findAllByRole('alert');
        for (const alert of alerts) expect(alert).toHaveTextContent(en.proactiveNotifications.engagement.loadFailed);
        expect(screen.queryByRole('table')).toBeNull();
        fireEvent.click(screen.getAllByRole('button', { name: 'Try again' })[0]);
        await screen.findByRole('table');
        expect(getWeek).toHaveBeenCalledTimes(2);
    });

    it('localizes the headings, explanation and zero-send rate in Chinese', async () => {
        await i18n.changeLanguage('zh-CN');
        render(<Week />);
        await screen.findByRole('table', { name: zh.proactiveNotifications.engagement.title });
        expect(screen.getByText(zh.proactiveNotifications.engagement.measurement)).toBeInTheDocument();
        expect(screen.getByText(zh.proactiveNotifications.engagement.noRate)).toBeInTheDocument();
        for (const label of Object.values(zh.proactiveNotifications.engagement.states)) {
            expect(screen.getByRole('columnheader', { name: label })).toBeInTheDocument();
        }
        expect(Object.keys(en.proactiveNotifications.engagement)).toEqual(Object.keys(zh.proactiveNotifications.engagement));
        expect(Object.keys(en.proactiveNotifications.engagement.states)).toEqual(Object.keys(zh.proactiveNotifications.engagement.states));
    });

    it("keeps the week's line, the one place it lives since Today dropped it", async () => {
        render(<Week table={false} />);
        expect(await screen.findByText('Isoform spoke up 10 times this week, you acted on 3.')).toBeInTheDocument();
        getWeek.mockResolvedValue({ ...fixture, sent: 1, actedOn: 0 });
        await act(async () => window.dispatchEvent(new Event('focus')));
        expect(screen.getByText('Isoform spoke up once this week, you acted on 0.')).toBeInTheDocument();
        getWeek.mockResolvedValue({ ...fixture, sent: 0, actedOn: 0, kinds: [] });
        await act(async () => window.dispatchEvent(new Event('focus')));
        expect(screen.getByText('Isoform did not speak up this week.')).toBeInTheDocument();
        await i18n.changeLanguage('zh-CN');
        expect(screen.getByText('Isoform 本周没有主动提醒。')).toBeInTheDocument();
    });

    it('stops showing counts when engagement is unavailable', async () => {
        getWeek.mockResolvedValueOnce({ ...fixture, available: false });
        render(<Week />);
        await waitFor(() => expect(screen.getByText(en.proactiveNotifications.unavailable)).toBeInTheDocument());
        expect(screen.queryByRole('table')).toBeNull();
    });
});
