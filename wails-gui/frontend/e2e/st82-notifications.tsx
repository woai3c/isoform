// Fictional Settings fixture for the ST82 light, dark and zh-CN renders.
import { createRoot } from 'react-dom/client';
import '@/index.css';

const params = new URLSearchParams(location.search);
if (params.get('theme')) localStorage.setItem('yansu.theme', params.get('theme')!);
else localStorage.removeItem('yansu.theme');
const kind = (name: string, sent: number) => ({
    kind: name, enabled: true, defaultOn: true, paused: false, quietOnToday: false,
    quiet: false, deliveryMode: 'normal', quietAllowed: true, canQuiet: true,
    quietSuggestionAllowed: true, sent, notificationSent: sent, notificationUseful: 0,
    notificationActed: 0, opened: 0, acted: 0, dismissed: 0, muted: 0,
    channels: ['computer'], tag: sent ? 'rare' : '',
});
const view = { available: true, level: 'more', kinds: [kind('brief_ready', 4), kind('brief_missed', 1), kind('meeting_prep', 0)],
    summary: { sent: 5, useful: 0, quietSent: 0 }, quietStart: '22:00', quietEnd: '08:00', dailyInterruptBudget: 10,
    digestTimes: ['08:30', '13:00', '18:00'], statsDays: 30 };
const reads: Record<string, unknown> = {
    GetEngagementSettings: view,
    GetEngagementWeekSummary: { available: true, days: 7, sent: 5, actedOn: 0, kinds: [] },
    GetPlatformPermissions: { platform: 'darwin', items: [] },
    GetMobileConnectionStatus: { paired: false }, GetTextChannelStatus: { available: false },
    GetSMSChannelStatus: { active: false }, GetEmailChannelStatus: { active: false }, LoadUIState: '',
};
(window as any).go = { main: { App: new Proxy({}, { get: (_, method: string) => async () => reads[method] ?? null }) } };
(window as any).runtime = new Proxy({}, { get: (_, name: string) => name === 'EventsOn' ? () => () => {} : () => () => {} });
const [{ default: i18n }, { ThemeProvider }, { NotificationsPage }] = await Promise.all([
    import('@/i18n'), import('@/hooks/useTheme'), import('@/components/settings/pages/notifications-page'),
]);
await i18n.changeLanguage(params.get('lang') ?? 'en');
createRoot(document.getElementById('root')!).render(
    <ThemeProvider><main className="settings-surface patina-ground h-screen overflow-auto bg-background p-8"><div className="mx-auto flex max-w-3xl flex-col gap-4"><NotificationsPage /></div></main></ThemeProvider>,
);
