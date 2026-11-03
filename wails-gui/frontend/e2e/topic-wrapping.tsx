// Isolated component fixture: actual topic renderers/CSS and synthetic host
// replies. Never start the app, its bridge, a provider or a user-data store.
import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '@/index.css';
import i18n from '@/i18n';
import { TodayTopicsGrid } from '@/components/today/TodayTopicsGrid';
import { TopicPanel } from '@/components/today/TopicPanel';
import { TopicComposer } from '@/components/today/TopicComposer';
import { TodayBriefSection } from '@/components/today/TodayBriefSection';
import { MainChatTopicView } from '@/components/main-chat/main-chat-topic-view';
import { MainChatAnswerChips } from '@/components/main-chat/main-chat-answer-chips';
import { MainChatTopicTags } from '@/components/main-chat/main-chat-topic-tags';
import { MainChatViewContext } from '@/components/main-chat/main-chat-view-context';
import type { MobileBriefDocument } from '@/lib/wails-shim-app';
import type { MainChatView } from '@/hooks/useMainChatView';
import type { TodayTopic, TopicChatItem } from '@/components/today/topics';

const noop = () => {};
const token = 'salesrabbitisoform'.repeat(20);
const sentence = 'Review the quoted thread "salesrabbit-isoform" and its release checklist before continuing. '.repeat(4).trim();
const topic: TodayTopic = { id: 'thread', kind: 'work', name: token, pinned: false, status: 'waiting', latest: { text: sentence }, next: { text: token } };
const items: TopicChatItem[] = [
    { id: 'event:thread', kind: 'event', at: 1, text: `${sentence} ${token}` },
    { id: 'message:user', kind: 'message', at: 2, message: { id: 'user', kind: 'user', sortOrder: 1, content: `${token} ${sentence}` } },
    { id: 'message:answer', kind: 'message', at: 3, message: { id: 'answer', kind: 'assistant', sortOrder: 2, content: `Summary: ${sentence}\n\nQuoted thread: \`${token}\`\n\n\`\`\`\n${token}\n\`\`\`` } },
];
const fixture = window as unknown as Record<string, any>;
fixture.runtime = { EventsOn: () => noop };
fixture.go = { main: { App: new Proxy({}, { get: (_, method: string) => async () => {
    if (method === 'ListMainChatTopicItems') return { items, hasMore: false, nextCursor: '' };
    return null;
} }) } };
const handlers = { onStartChat: noop, onRunNow: noop, onOpenMemory: noop, onOpenAutomations: noop, onOpenActivity: noop, onOpenPeople: noop, onOpenTopic: noop };
const chat = { items, status: 'ready', hasMore: false, waiting: false, pending: null };
const date = new Date(2026, 9, 23, 9);
const brief: MobileBriefDocument = { version: 1, briefId: 'fixture', slot: 'morning', localDate: '2026-10-23', asOf: date.toISOString(), coldStart: false,
    narrativeStatus: 'ok', narrative: [{ text: sentence, refs: ['e1'], topicId: topic.id, topicName: token }], sections: { moved: [], doing: [], watching: [], meetings: [], people: [], openLoops: [], learned: [], deliverables: [], schedules: [] }, dayShape: null,
    evidence: [{ ref: 'e1', sourceKind: 'calendar', sourceId: 'fixture', label: 'Fixture', observedAt: '' }],
    cards: [token, sentence].map((label, index) => ({ id: `q${index}`, kind: 'question', mode: 'send', label, message: label, refs: ['e1'], paragraph: 0 })) };
await i18n.changeLanguage(new URLSearchParams(location.search).get('language') ?? 'en');
function GridFixture() {
    const [refreshing, setRefreshing] = useState(false);
    fixture.setTopicRefreshing = setRefreshing;
    return <TodayTopicsGrid topics={[topic, { ...topic, id: 'sentence', name: sentence }]} status="ready" refreshing={refreshing} selectedId={null} onPick={noop} onRetry={noop} onEdit={noop} now={date} />;
}
createRoot(document.getElementById('root')!).render(
    <main className="today-page-content ml-[224px] h-screen min-w-0 overflow-y-auto bg-background text-ink" data-testid="wrapping-fixture">
        <div className="mx-auto min-w-0 max-w-[760px] px-6 py-4">
            <GridFixture />
            <TodayBriefSection view={{ brief, empty: false, building: false, enabled: true, nextAt: '', nextSlot: '', missing: [] }} loading={false} make="idle" nextAllowedAt="" onMakeNow={noop} handlers={handlers} now={date} />
            <MainChatViewContext.Provider value={{ today: '2026-10-23', hideRoutine: false, topicNames: new Map([[topic.id, token]]), chips: { answer: [token, sentence] }, onFillChip: noop }}>
                <MainChatAnswerChips itemId="answer" /><MainChatTopicTags topicIds={[topic.id]} />
            </MainChatViewContext.Provider>
        </div>
        <div className="h-[800px] w-[360px] max-w-full" data-testid="narrow-detail">
            <TopicPanel topic={topic} variant="pinned" draft={token} prepared={false} onDraftChange={noop} handlers={handlers} observationPaused={false} />
        </div>
        <div className="flex h-[1000px] min-w-0 flex-col">
            <MainChatTopicView view={{ topic, topicChat: chat } as unknown as MainChatView} onShowEverything={noop} />
        </div>
        <div className="w-[360px] max-w-full">
            <TopicComposer draft={token} prepared={false} onDraftChange={noop} onSend={noop} sending={false} inputLabel="Message" placeholder="Message" sendLabel="Send" note={sentence} chip={{ name: token, removeLabel: 'Remove topic', onRemove: noop }} />
        </div>
    </main>,
);
