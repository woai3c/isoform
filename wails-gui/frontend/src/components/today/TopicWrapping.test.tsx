import { act, render, screen, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { TodayTopicsGrid } from './TodayTopicsGrid';
import { TopicPanel } from './TopicPanel';
import { TopicComposer } from './TopicComposer';
import { MainChatTopicView } from '@/components/main-chat/main-chat-topic-view';
import { MainChatAnswerChips } from '@/components/main-chat/main-chat-answer-chips';
import { MainChatTopicTags } from '@/components/main-chat/main-chat-topic-tags';
import { MainChatViewContext } from '@/components/main-chat/main-chat-view-context';
import type { MainChatView } from '@/hooks/useMainChatView';
import type { TodayTopic, TopicChatItem } from './topics';

vi.mock('@/lib/wails-shim-app', () => ({ ListMainChatTopicItems: vi.fn().mockResolvedValue({ items: [], hasMore: false, nextCursor: '' }) }));
vi.mock('@/lib/wails-shim-runtime', () => ({ EventsOn: () => () => {} }));

const token = 'salesrabbitisoform'.repeat(40);
const sentence = 'Review the quoted thread "salesrabbit-isoform" and its release checklist before continuing. '.repeat(12).trim();
const noop = () => {};

beforeEach(async () => { await i18n.changeLanguage('en'); });

// jsdom has no line layout. Guard the sizing/wrapping contract and complete
// text here; the isolated browser harness checks actual bounds and scroll size.
it.each([720, 768, 920, 1024, 1280, 1440, 1920])('keeps long topic text wrappable at a %i px window', async (width) => {
    const topic: TodayTopic = { id: 'thread', kind: 'work', name: token, pinned: false, latest: { text: sentence, at: new Date().toISOString() }, next: { text: token } };
    const rows: TopicChatItem[] = [
        { id: 'event:thread', kind: 'event', at: 0, text: `${sentence} ${token}` },
        { id: 'message:user', kind: 'message', at: 0, message: { id: 'user', kind: 'user', sortOrder: 1, content: `${token} ${sentence}` } },
        { id: 'message:answer', kind: 'message', at: 0, message: { id: 'answer', kind: 'assistant', sortOrder: 2, content: `Summary: ${sentence}\n\nQuoted thread: \`${token}\`\n\n\`\`\`\n${token}\n\`\`\`` } },
    ];
    const chat = { items: rows, status: 'ready', hasMore: false, waiting: false, pending: null };
    const { container } = render(<div style={{ width }}>
        <TodayTopicsGrid topics={[topic]} status="ready" selectedId={null} onPick={noop} onRetry={noop} onEdit={noop} />
        <TopicPanel topic={topic} variant="pinned" draft="" prepared={false} onDraftChange={noop}
            handlers={{ onStartChat: noop, onRunNow: noop, onOpenMemory: noop, onOpenAutomations: noop, onOpenActivity: noop, onOpenPeople: noop }} observationPaused={false} />
        <MainChatTopicView view={{ topic, topicChat: chat } as unknown as MainChatView} onShowEverything={noop} />
        <TopicComposer draft={token} prepared={false} onDraftChange={noop} onSend={noop} sending={false}
            inputLabel="Message" placeholder="Message" sendLabel="Send" note={sentence} chip={{ name: token, removeLabel: 'Remove topic', onRemove: noop }} />
        <MainChatViewContext.Provider value={{ today: '', hideRoutine: false, topicNames: new Map([['thread', token]]), chips: { answer: [token, sentence] }, onFillChip: noop }}>
            <MainChatAnswerChips itemId="answer" />
            <MainChatTopicTags topicIds={['thread']} />
        </MainChatViewContext.Provider>
    </div>);
    await act(async () => {});
    const card = screen.getByTestId('today-topic-card');
    expect(card).toHaveClass('min-w-0', 'max-w-full', 'whitespace-normal', '[overflow-wrap:anywhere]');
    // Review v1.1.8 replaces an overlong line with a timed Active line.
    expect(card).toHaveTextContent(topic.name);
    expect(card).not.toHaveTextContent(topic.latest!.text);
    expect(within(card).getByTestId('today-topic-line')).toHaveTextContent(/^Active · /);
    for (const child of Array.from(card.children).slice(1)) expect(child).toHaveClass('min-w-0', 'max-w-full');
    const panel = screen.getByTestId('topic-panel');
    expect(panel).toHaveClass('min-w-0', '[overflow-wrap:anywhere]');
    expect(within(panel).getByRole('heading')).toHaveTextContent(token);
    const detail = screen.getByTestId('main-chat-topic-view');
    expect(detail).toHaveClass('min-w-0', '[overflow-wrap:anywhere]');
    expect(within(detail).getByTestId('topic-panel-event').querySelector('.flex-1')).toHaveClass('min-w-0', '[overflow-wrap:anywhere]');
    expect(within(detail).getByTestId('topic-panel-user')).toHaveTextContent(`${token} ${sentence}`);
    expect(within(detail).getByTestId('topic-panel-isoform').querySelector('pre')?.parentElement).toHaveClass('[&_pre]:whitespace-pre-wrap');
    const chip = screen.getByTestId('topic-composer-chip');
    expect(chip).toHaveClass('min-h-6', 'max-w-full');
    expect(chip.firstChild).toHaveClass('min-w-0', 'whitespace-normal', '[overflow-wrap:anywhere]');
    const suggestions = screen.getByRole('group', { name: 'Prepared requests' });
    expect(within(suggestions).getAllByRole('button').map(button => button.textContent)).toEqual([token, sentence]);
    for (const button of within(suggestions).getAllByRole('button')) expect(button).toHaveClass('h-auto', 'min-w-0', 'max-w-full', 'whitespace-normal', '[overflow-wrap:anywhere]');
    expect(container.querySelector('[data-main-chat-topic-tags] li')).toHaveClass('min-h-5', 'min-w-0', 'max-w-full', '[overflow-wrap:anywhere]');
    expect(container.querySelector('.truncate, .line-clamp-1, .line-clamp-2')).toBeNull();
});
