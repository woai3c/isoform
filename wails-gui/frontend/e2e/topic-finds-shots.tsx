// Design review shots for the Today "New on your topics" section: the loading
// skeleton and a saved view. The section is the real component; the data is a
// fixture and nothing is read from the app.
import React from 'react';
import { createRoot } from 'react-dom/client';
import i18n from '@/i18n';
import '@/index.css';
import { TopicFindsSection } from '@/components/today/TopicFindsSection';
import type { TopicFind, TopicFindsView } from '@/lib/wails-shim-app';
import type { UseTopicFindsResult } from '@/hooks/useTopicFinds';

const noop = () => {};
(window as any).runtime = { EventsOn: () => noop };
(window as any).go = { main: { App: new Proxy({}, { get: () => async () => null }) } };

const NOW = new Date(2026, 10, 5, 9, 30);
const finds: TopicFind[] = [
    { id: 'f1', topicId: 't1', topic: 'Wails drag regions', title: 'Wails 2.12.1 released', source: 'GitHub', url: 'https://example.com/a', whatsNew: 'Changes how the drag regions are read', lookedAt: new Date(2026, 10, 4, 16).toISOString(), publishedAt: '2026-11-03', score: 0.9 },
    { id: 'f2', topicId: 't2', topic: 'Silent speech', title: 'EMG wearable notes', source: 'arXiv', url: 'https://example.com/b', whatsNew: 'A new sensor layout', lookedAt: new Date(2026, 10, 4, 12).toISOString(), publishedAt: '2026-11-02', score: 0.8 },
    { id: 'f3', topicId: 't3', topic: 'Launch week', title: 'Release checklist update', source: 'Docs', url: 'https://example.com/c', whatsNew: 'Adds the signing step', lookedAt: new Date(2026, 10, 3, 18).toISOString(), publishedAt: '2026-11-01', score: 0.7 },
];

function state(view: Partial<TopicFindsView> | null, loading: boolean): UseTopicFindsResult {
    return {
        view: view === null ? null : { enabled: true, status: 'ready', items: [], topics: ['Wails drag regions'], ...view },
        loading,
        loadFailed: false,
        failure: null,
        findNew: async () => {},
        feedback: async () => true,
        refresh: noop,
    };
}

const params = new URLSearchParams(location.search);
const which = params.get('state') ?? 'saved';
const theme = params.get('theme');
if (theme) document.documentElement.dataset.theme = theme;
const finds_state = which === 'skeleton'
    ? state(null, true)
    : state({ status: 'ready', items: finds }, false);

void i18n.changeLanguage('en').then(() => {
    createRoot(document.getElementById('root')!).render(
        <div className="mx-auto flex max-w-3xl flex-col gap-4 bg-background p-8 text-ink">
            <TopicFindsSection finds={finds_state} onOpenTopics={noop} now={NOW} />
        </div>,
    );
});
