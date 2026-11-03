// Real components and production CSS with synthetic reads. No native app or backend.
import React from 'react';
import { createRoot } from 'react-dom/client';
import '@/index.css';
import i18n from '@/i18n';
import { TodayPage } from '@/components/today/TodayPage';
import { todayNextBrief } from '@/components/today/today-next.fixture';
import { ActivityDayView } from '@/components/activity-day-view';
import { stitchedFixture } from '@/components/activity-stitched.fixture';
import { MemoryPage } from '@/components/memory-page';
import { AppearanceSettings } from '@/components/settings/pages/general-page';
import { ThemeProvider } from '@/hooks/useTheme';
import type { TodayActionHandlers } from '@/components/today/types';

const params = new URLSearchParams(location.search);
const theme = params.get('theme') ?? 'paper';
const language = params.get('lang') ?? 'en';
const slot = params.get('slot') ?? 'morning';
const state = params.get('state') ?? 'ready';
const noop = () => {};
const day = new Date(2026, 9, 19);
const work = stitchedFixture(day);
const belief = { id: 'writing', title: 'Writing style', content: '# Writing style\nI prefer detailed replies.', dimension: 'user_insight', source: 'activity', updatedAt: Date.now(), modTime: Date.now(), sizeBytes: 50, apps: [], activitySessionIds: [] };
const reads: Record<string, unknown> = {
    GetLatestBrief: { brief: state === 'empty' ? null : { ...todayNextBrief, slot, displaySlot: slot }, building: false, enabled: true, missing: [] },
    GetTodayDayShape: null, GetMobileBriefHealth: null, GetCaptureCounts: null,
    GetTodayNews: { date: '', status: 'ready', items: [], updatedAt: 0 }, GetTodayNewsHistory: [],
    GetTodaySuggestion: { available: false }, GetGoalCheckIn: null, GetTopicFinds: { status: 'off', items: [], topics: [] },
    GetTodayTopics: [], GetTodayQuietLines: [], IsClosedEyesModeEnabled: false,
    GetMainChat: { session: { localId: 'main' } }, LoadStreamItemsPage: { items: [], hasMore: false }, ListMainChatAnswerChips: {},
    GetActivitiesInRange: state === 'empty' ? [] : work.activities,
    GetDayThreads: { grouped: true, threads: state === 'empty' ? [] : work.threads }, GetRecentMeetingNotes: [], GetAppIcons: {},
    GetActivityNoticed: { items: [] }, GetTelemetryTier: 'detailed',
    GetMemoryEntries: [belief],
    GetMemoryStatus: { files: 1, chunks: 1, vectorReady: true },
    GetAboutYou: { beliefs: [], newThisWeek: [], totals: { beliefs: 1, confirmed: 0 }, storage: { files: 1, bytes: 50, chunks: 1, vectorReady: true } },
    GetAboutYouPortrait: { state: state === 'ready' ? 'ready' : state === 'empty' ? 'none' : state, generatedAt: Date.now(), segments: state === 'ready' ? [
        { text: language === 'en' ? 'You ' : '你', memoryIds: [] },
        { text: language === 'en' ? 'prefer detailed replies' : '喜欢详细的回复', memoryIds: ['writing'] },
        { text: language === 'en' ? ' and plan your week on paper.' : '，并在纸上规划每一周。', memoryIds: [] },
    ] : [] },
    GetMemoryEvidenceReadable: [], ListMemoryVersions: [], GetEntryUses: { total: 0, lastAt: 0, surfaces: [] },
    GetThemePreference: theme, LoadUIState: '',
};
(window as any).runtime = { EventsOn: () => noop };
(window as any).go = { main: { App: new Proxy({}, { get: (_, method: string) => async () => {
    if (method === 'GetAboutYouPortrait' && state === 'loading') return new Promise(() => {});
    if (method === 'GetAboutYouPortrait' && state === 'failed') throw new Error('Fixture read failed');
    return reads[method] ?? null;
} }) } };
localStorage.setItem('yansu.theme', theme);
await i18n.changeLanguage(language);
document.documentElement.dataset.theme = theme;
const handlers: TodayActionHandlers = {
    onStartChat: noop, onRunNow: noop, onOpenMemory: noop, onOpenAutomations: noop,
    onOpenActivity: noop, onOpenPeople: noop, onOpenSettings: noop,
};
const page = params.get('page');
createRoot(document.getElementById('root')!).render(
    <main className="flex h-screen flex-col bg-background p-6 text-ink">
        {page === 'today' ? <TodayPage handlers={handlers} />
            : page === 'activity' ? <ActivityDayView date={day} searchQuery="" bridgePort={0} dailyData={null} dayLoading={false}
                automationRuns={[]} knowledge={[]} memoryEntries={[]} hourRange={{ start: 6, end: 23 }} maxHourly={0}
                section="workedOn" workedOnView={params.has('recorded') ? 'recorded' : 'stitched'} reloadDailySummary={async () => {}}
                onOpenSession={noop} onOpenRunDetail={noop} />
            : page === 'settings' ? <ThemeProvider><AppearanceSettings readingWidth /></ThemeProvider>
            : <MemoryPage />}
    </main>,
);
