import React from 'react';
import { createRoot } from 'react-dom/client';
import '@/index.css';
import i18n from '@/i18n';
import { ThemeProvider } from '@/hooks/useTheme';
import { MainChatPanel } from '@/components/today/MainChatPanel';

const params = new URLSearchParams(location.search);
const theme = params.get('theme') ?? 'paper';
const lang = params.get('lang') ?? 'en';
document.documentElement.dataset.theme = theme;
localStorage.setItem('yansu.theme', theme);
const reads: Record<string, unknown> = {
    GetThemePreference: theme,
    GetNeedsYou: { items: [{ id: 'need-1', kind: 'work_blocked', sentence: 'Review the plan', createdAt: Date.now(), targetKind: 'chat_item', targetId: 'item-1' }], putOff: [], total: 1 },
    GetMainChat: { session: { localId: 'main' } },
    GetMainChatWaiting: { sessionId: 'main', count: 3, items: [], revision: 1, ready: true },
    LoadStreamItemsPage: { items: [], hasMore: false, nextCursor: -1 },
    ListMainChatAnswerChips: {},
};
const host = window as unknown as Record<string, any>;
host.go = { main: { App: new Proxy({}, { get: (_, method: string) => async () => structuredClone(reads[method] ?? null) }) } };
host.runtime = new Proxy({}, { get: () => () => {} });
await i18n.changeLanguage(lang);
createRoot(document.getElementById('root')!).render(<ThemeProvider><main className="h-screen bg-background p-8"><div className="h-full w-[500px] border border-border"><MainChatPanel topicsFailed={false} onRetryTopics={() => {}} draft="" prepared={false} onDraftChange={() => {}} handlers={{ onOpenTasks: () => {} } as never} /></div></main></ThemeProvider>);
