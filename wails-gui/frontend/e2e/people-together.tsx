// Fictional data for the Who you know Together screenshots.
import React from 'react';
import { createRoot } from 'react-dom/client';
import '@/index.css';
import i18n from '@/i18n';
import { ThemeProvider } from '@/hooks/useTheme';
import { PeoplePage } from '@/components/people-page';

const params = new URLSearchParams(location.search);
const theme = params.get('theme') ?? 'paper';
const chinese = params.get('lang') === 'zh-CN';
const name = chinese ? '王晓雨' : 'Mira Castell';
const now = Date.now();
localStorage.setItem('yansu.theme', theme);
document.documentElement.dataset.theme = theme;
const card = { id: 'mira', kind: 'contact', name, relationship: '', photo: '', loops: [], lastSeenAt: now - 3600000, lastSeenApp: 'WeChat' };
const detail = { id: 'mira', name, relationship: '', notes: '', researchSummary: '', loops: [], sharedTopics: [], seenIn: ['WeChat'], messageCount: 2, together: [
    { at: now - 3600000, kind: 'message', title: '', app: 'wechat', direction: 'in', durationMs: 0 },
    { at: now - 7200000, kind: 'activity', title: chinese ? '和王晓雨聊了发布时间' : 'Messaged Mira about the release timing', app: 'WeChat', durationMs: 60000 },
    { at: now - 86400000, kind: 'meeting', title: chinese ? '项目讨论' : 'Project discussion', app: 'calendar', durationMs: 2700000 },
] };
const reads: Record<string, unknown> = {
    GetThemePreference: theme, GetFirstWeekDay: -1, GetAssistantHistory: null, GetCalendarFirstDay: null,
    ListPeopleOverview: { headline: { repliesOwed: 0, thingsOwed: 0, peopleWaitingOnYou: 0 }, knownCount: 1, duplicates: [], open: [], everyone: [card] },
    ListPrincipalsWithStats: [], GetPersonDetail: detail, ListPrincipalActivitySessions: [], GetContactsAccessStatus: null,
    IsClosedEyesModeEnabled: false, GetPersonResearch: null, GetVoiceStatus: null,
};
const host = window as unknown as Record<string, any>;
host.go = { main: { App: new Proxy({}, { get: (_, method: string) => async () => structuredClone(reads[method] ?? null) }) } };
host.runtime = new Proxy({}, { get: () => () => {} });
await i18n.changeLanguage(chinese ? 'zh-CN' : 'en');
createRoot(document.getElementById('root')!).render(<ThemeProvider><main className="h-screen w-screen bg-background"><PeoplePage active initialSelection={{ personId: 'mira' }} /></main></ThemeProvider>);
