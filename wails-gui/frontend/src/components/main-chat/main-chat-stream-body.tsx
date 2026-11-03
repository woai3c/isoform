import { useEffect, useMemo, type ComponentProps, type ReactNode } from 'react';
import { MainChatSidePanelView } from './main-chat-side-panel';
import { useMainChatSidePanel } from '@/lib/main-chat-side-panel';
import { ChatFailureLine } from '@/components/chat-failure-line';
import { useChatPlaceFailure } from '@/lib/chat-failure';
import { useTranslation } from 'react-i18next';
import { FirstDayChatStarters } from './first-day-starters';
import { StreamBody } from '@/components/stream-body';
import type { MainChatView } from '@/hooks/useMainChatView';
import { MainChatFilterRow } from './main-chat-filter-row';
import { MainChatTopicView } from './main-chat-topic-view';
import type { MainChatStreamView } from './main-chat-view-context';
import { MainChatItemPanel } from '@/components/main-chat-item-panel';
import { useMainChatItemPanel } from '@/hooks/useMainChatItemPanel';
import { getMainChatProactive, type MainChatReportTarget } from '@/lib/stream-helpers';

type StreamBodyProps = ComponentProps<typeof StreamBody>;

/** Main chat keeps a readable column regardless of the other readers' width preference. */
export function MainChatPageColumn({ enabled, children, onOpenTarget }: { enabled: boolean; children: ReactNode; onOpenTarget?: (target: MainChatReportTarget) => void }) {
    const panel = useMainChatItemPanel('chat');
    const side = useMainChatSidePanel();
    const placeFailure = useChatPlaceFailure();
    const { t } = useTranslation('chat');
    const panelOpen = enabled && Boolean(panel.openId);
    const sideOpen = enabled && !panelOpen && side !== null;
    return (
        <div
            data-testid={enabled ? 'main-chat-column' : undefined}
            className="mx-auto flex min-w-0 min-h-0 w-full max-w-[1188px] flex-1 flex-row"
        >
            <div className="mx-auto flex min-w-0 min-h-0 w-full max-w-[768px] flex-1 flex-col">
                {enabled && placeFailure && (
                    <ChatFailureLine testId="place" className="px-6 pt-3" text={t(placeFailure.key)} onRetry={placeFailure.retry} />
                )}
                {children}
            </div>
            {panelOpen && <MainChatItemPanel surface="chat" onOpenTarget={onOpenTarget} />}
            {sideOpen && side && <MainChatSidePanelView panel={side} />}
        </div>
    );
}

/**
 * The chat's stream. For the main chat (RC 2 D1) it adds the filter row
 * above the scroll area, the summary line, day dividers and folds, and shows
 * the waiting cards or a topic's rows when a filter is on. Any other chat is
 * the plain StreamBody.
 */
export function MainChatStreamBody({ mainChat, onPrepare, ...props }: StreamBodyProps & {
    /** The main chat's view; absent for every other chat. */
    mainChat?: MainChatView;
    /** Sends words as the person's message at once (an answer chip or a starter, Chat K4). */
    onPrepare?: (text: string) => void | boolean | Promise<boolean>;
}) {
    const view = mainChat;
    const today = view?.today ?? '';
    const enabled = Boolean(view);
    const hideRoutine = view?.hideRoutine ?? false;
    const topicNames = view?.topicNames;
    const chips = view?.chips;
    const firstReadId = props.items?.find((item) => getMainChatProactive(item)?.kind === 'first_read')?.id;
    const streamView = useMemo<MainChatStreamView | undefined>(() => enabled && today && topicNames && chips ? {
        today,
        hideRoutine,
        topicNames,
        chips,
        onFillChip: onPrepare,
        chipsSend: Boolean(onPrepare),
        firstReadStarter: firstReadId ? { itemId: firstReadId, items: props.items } : undefined,
    } : undefined, [enabled, today, hideRoutine, topicNames, chips, onPrepare, firstReadId, props.items]);
    // Search opened a message (RC 2 D3): a filter would hide it, so the
    // view goes back to Everything, where the stream scrolls to it.
    const focusSignal = props.focusItem?.signal;
    const filterKind = view?.filter.kind;
    const setFilter = view?.setFilter;
    useEffect(() => {
        if (focusSignal && filterKind && filterKind !== 'all') setFilter?.({ kind: 'all' });
        // Only a new request moves the view; choosing a filter later must not.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [focusSignal]);

    if (!view || !streamView) return <StreamBody {...props} />;

    const overview = view.overview;
    const filters = (
        <MainChatFilterRow
            filter={view.filter}
            onFilter={view.setFilter}
            topics={view.workTopics}
            hideRoutine={view.hideRoutine}
            routineCount={overview ? overview.routine : null}
            hideBusy={view.hideBusy}
            hideFailed={view.hideFailed}
            onToggleHide={(hide) => void view.setHideRoutine(hide)}
        />
    );
    const showEverything = () => view.setFilter({ kind: 'all' });
    if (view.filter.kind === 'topic') {
        return (
            <div className="flex min-h-0 flex-1 flex-col">
                {filters}
                <MainChatTopicView view={view} onShowEverything={showEverything} wideMode={props.wideMode} />
            </div>
        );
    }

    // The first day: Isoform's starters under the messages (handoff §4).
    const starters = enabled && !firstReadId ? <FirstDayChatStarters items={props.items} onPrepare={onPrepare} /> : null;
    return (
        <StreamBody
            {...props}
            toolbar={filters}
            mainChatView={streamView}
            footer={props.footer || starters ? <>{props.footer}{starters}</> : undefined}
        />
    );
}
