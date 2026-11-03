import { useEffect, useRef, type ReactNode } from 'react';
import { ChatFailureLine } from '@/components/chat-failure-line';
import { useTranslation } from 'react-i18next';
import { AlertCircle, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { TopicRow } from '@/components/today/TopicPanel';
import { TopicComposer } from '@/components/today/TopicComposer';
import { useObservationPaused } from '@/components/today/useObservationPaused';
import { QueuedMessageList } from '@/components/composer-queue';
import type { MainChatView } from '@/hooks/useMainChatView';
import type { QueuedMessage } from '@/hooks/useComposerInput';

type Translate = (key: string, options?: Record<string, unknown>) => string;

/**
 * The main chat filtered to one work topic (RC 2 D1): the same rows as the
 * topic panel on Today (its messages and the Activity events behind them),
 * at the main chat's full width. It is never a side chat; "Show everything"
 * goes back to the whole main chat.
 */
export function MainChatTopicView({ view, summary, onShowEverything, wideMode = false }: {
    view: MainChatView;
    /** "In your chat today", as above every filter. */
    summary?: ReactNode;
    onShowEverything: () => void;
    wideMode?: boolean;
}) {
    const { t, i18n } = useTranslation('today');
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const { t: tChat } = useTranslation('chat');
    const chat = view.topicChat;
    const topic = view.topic;
    const listRef = useRef<HTMLDivElement>(null);

    // Follow the newest row as replies arrive.
    const lastRow = chat.items[chat.items.length - 1]?.id ?? '';
    useEffect(() => {
        const list = listRef.current;
        if (list) list.scrollTop = list.scrollHeight;
    }, [lastRow, chat.pending?.state, chat.waiting]);

    if (!topic) return (
        <div className={cn('flex min-h-0 min-w-0 flex-1 flex-col gap-3 px-8 py-6', !wideMode && 'mx-auto w-full max-w-3xl')} data-testid="main-chat-topic-refreshing">
            <p role="status" aria-busy="true" className="text-[13.5px] text-muted">{tChat('mainChat.view.topicChecking')}</p>
            <Button variant="link" className="self-start p-0 text-[12.5px] font-medium" onClick={onShowEverything}>
                {tr('topicPanel.showEverything')}
            </Button>
        </div>
    );
    return (
        <div ref={listRef} className="min-h-0 min-w-0 flex-1 overflow-y-auto [overflow-wrap:anywhere]" data-testid="main-chat-topic-view">
            <div className={cn('flex min-w-0 flex-col gap-3.5 px-8 [&>*]:shrink-0 pb-6 pt-6', !wideMode && 'mx-auto w-full max-w-3xl')}>
                {summary}
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-lg bg-panel-warm px-3.5 py-2.5 text-[12.5px] text-muted">
                    <span className="min-w-0 max-w-full">{tr('topicPanel.showingOnly', { name: topic.name })}</span>
                    <Button variant="link" className="h-auto p-0 text-[12.5px] font-medium" onClick={onShowEverything}>
                        {tr('topicPanel.showEverything')}
                    </Button>
                </div>

                {chat.status === 'loading' && (
                    <div aria-busy="true" className="flex flex-col gap-2.5" data-testid="main-chat-topic-loading">
                        <span className="h-2.5 w-3/5 rounded-md bg-surface-active" />
                        <span className="h-3 w-[90%] rounded-md bg-surface-active" />
                        <span className="h-3 w-3/4 rounded-md bg-surface-active" />
                    </div>
                )}
                {chat.status === 'failed' && (
                    <div className="flex flex-col gap-2.5" data-testid="main-chat-topic-failed">
                        <ChatFailureLine testId="topic" className="text-[13.5px]" text={tChat('failure.loadTopic')} onRetry={async () => { await chat.reload(); return chat.status !== 'failed'; }} />
                        <div className="flex flex-wrap gap-2">
                            <Button size="sm" variant="quiet" onClick={onShowEverything}>{tr('topicPanel.showEverything')}</Button>
                        </div>
                    </div>
                )}
                {chat.status === 'withdrawn' && (
                    <div className="flex flex-col gap-2.5" data-testid="main-chat-topic-withdrawn">
                        <p role="alert" className="flex items-start gap-2 text-[13.5px] text-ink">
                            <AlertCircle className="mt-0.5 size-[15px] shrink-0 text-subtle" />
                            {tr('topicPanel.withdrawn')}
                        </p>
                        <div>
                            <Button size="sm" variant="quiet" onClick={onShowEverything}>{tr('topicPanel.showEverything')}</Button>
                        </div>
                    </div>
                )}
                {chat.status === 'ready' && chat.hasMore && (
                    <div>
                        <Button size="sm" variant="quiet" disabled={chat.loadingMore} aria-busy={chat.loadingMore} onClick={() => void chat.loadMore()}>
                            {chat.loadingMore && <Loader2 className="size-3.5 animate-spin" />}
                            {tr('topicPanel.earlier')}
                        </Button>
                    </div>
                )}
                {chat.status === 'ready' && chat.items.length === 0 && !chat.hasMore && !chat.pending && !chat.waiting && (
                    <p className="self-center py-6 text-[13.5px] text-muted" data-testid="main-chat-topic-empty">{tr('topicPanel.empty', { name: topic.name })}</p>
                )}
                {chat.items.map((item) => (
                    <TopicRow key={item.id} item={item} language={i18n.language} tr={tr} />
                ))}
                {chat.pending && (
                    <div className="flex flex-col items-end gap-1.5" data-testid="main-chat-topic-pending">
                        <div className={cn('min-w-0 max-w-[80%] whitespace-pre-wrap rounded-xl border border-border bg-panel-user px-[13px] py-[9px] text-[14px] text-ink', chat.pending.state === 'sending' && 'opacity-70')}>
                            {chat.pending.text}
                        </div>
                        {chat.pending.state === 'failed' && (
                            <p role="alert" className="text-[12.5px] text-subtle">{tChat('errors.failedSend.line')}</p>
                        )}
                    </div>
                )}
                {(chat.pending?.state === 'sending' || chat.waiting) && (
                    <p role="status" className="flex items-center gap-2 text-[13.5px] text-muted">
                        <Loader2 className="size-3.5 animate-spin" />
                        {tr('topicPanel.working')}
                    </p>
                )}
            </div>
        </div>
    );
}

/**
 * The main chat's box with a work topic attached: words go to the main chat
 * through SendMainChatWithTopic, and the chip on the box drops the topic.
 * While observation is paused a topic send is refused, so the words go to the
 * plain box instead, unsent. Each topic keeps its own draft.
 */
export function MainChatTopicBox({
    view,
    onUsePlainBox,
    streaming = false,
    onStop,
    queuedMessages = [],
    onRemoveQueued,
    onEditQueued,
    wideMode = false,
}: {
    view: MainChatView;
    /** Puts words in the main chat's plain box, unsent. */
    onUsePlainBox: (text: string) => void;
    /** A reply is running in the main chat. */
    streaming?: boolean;
    /** Stops it, as the plain box's Stop does. */
    onStop?: () => void;
    /** The plain box's queue; it stays in view while a topic is attached. */
    queuedMessages?: readonly QueuedMessage[];
    onRemoveQueued?: (index: number) => void;
    /** Puts a queued message back in the plain box to edit. */
    onEditQueued?: (index: number) => void;
    wideMode?: boolean;
}) {
    const { t } = useTranslation('today');
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const { t: tChat } = useTranslation('chat');
    const { t: tApp } = useTranslation('app');
    const paused = useObservationPaused() === true;
    const mounted = useRef(true);
    useEffect(() => {
        // StrictMode mounts twice in development; mark it mounted again.
        mounted.current = true;
        return () => { mounted.current = false; };
    }, []);
    const topic = view.topic;
    if (view.topicChat.status === 'withdrawn' || (!topic && !view.topicPending)) return null;
    const { text: draft, prepared } = view.topicDraft;
    const setDraft = view.setTopicDraft;

    const send = async () => {
        const words = draft.trim();
        if (!topic || !words || view.topicChat.pending?.state === 'sending') return;
        if (paused) {
            setDraft('', false);
            view.detach();
            onUsePlainBox(words);
            return;
        }
        // While a reply runs the words wait in the box; Stop or the reply's
        // end frees Send.
        if (streaming) return;
        setDraft('', false);
        const result = await view.topicChat.send(words);
        // Not sent: the words go back in this topic's box.
        if (result === 'failed' && mounted.current) setDraft(words, false);
    };
    const detach = () => {
        // The words stay with the topic for when it is attached again.
        view.detach();
    };

    const note = !topic
        ? tChat('mainChat.view.topicChecking')
        : paused
            ? tr('topicPanel.pausedNote')
            : streaming
                ? tChat('mainChat.view.topicBusy')
                : prepared
                    ? tChat('mainChat.view.notePrepared')
                    : tr('topicPanel.noteTopic');
    const stopLabel = queuedMessages.length > 0
        ? tApp('composer.actions.stopStreamingQueued', { count: queuedMessages.length })
        : tApp('composer.actions.stopStreaming');
    return (
        <div className={cn('flex flex-col gap-2 px-6 pb-4', !wideMode && 'mx-auto w-full max-w-3xl')} data-testid="main-chat-topic-box">
            {onRemoveQueued && onEditQueued && (
                <QueuedMessageList
                    messages={queuedMessages}
                    onRemove={onRemoveQueued}
                    onEdit={(index) => {
                        // Editing happens in the plain box, without the topic.
                        view.detach();
                        onEditQueued(index);
                    }}
                />
            )}
            <TopicComposer
                draft={draft}
                prepared={prepared}
                onDraftChange={setDraft}
                onSend={() => void send()}
                sending={view.topicChat.pending?.state === 'sending'}
                inputLabel={tr('topicPanel.inputLabel')}
                placeholder={topic ? tr('topicPanel.placeholder', { name: topic.name }) : tr('mainChatPanel.placeholder')}
                sendLabel={paused ? tr('topicPanel.continueInMainChat') : tr('topicPanel.send')}
                note={note}
                noteTestId="main-chat-topic-note"
                chip={topic ? { name: topic.name, removeLabel: tChat('mainChat.view.detachTopic', { name: topic.name }), onRemove: detach } : undefined}
                disabled={!topic}
                stop={streaming && onStop ? { label: stopLabel, onStop } : undefined}
            />
        </div>
    );
}
