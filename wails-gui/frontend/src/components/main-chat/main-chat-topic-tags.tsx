import { useTranslation } from 'react-i18next';
import { isDisplayTopic } from '@/components/today/topics';
import { cn } from '@/lib/utils';
import { useMainChatStreamView } from './main-chat-view-context';

/** The topic tags on one of Isoform's cards or report lines, by name. A tag
 *  whose topic the registry does not list (or any tag outside the main chat)
 *  shows nothing; an id is never shown. */
export function MainChatTopicTags({ topicIds, className }: { topicIds?: readonly string[]; className?: string }) {
    const { t } = useTranslation('chat');
    const view = useMainChatStreamView();
    const tags = (topicIds ?? [])
        .map((id) => ({ id, name: view?.topicNames.get(id) }))
        .filter((tag): tag is { id: string; name: string } => isDisplayTopic(tag))
        .slice(0, 2);
    if (tags.length === 0) return null;
    return (
        <ul aria-label={t('mainChat.view.topicTags')} className={cn('flex min-w-0 flex-wrap gap-1.5', className)} data-main-chat-topic-tags>
            {tags.map(({ id, name }) => (
                <li
                    key={id}
                    className="inline-flex min-h-5 min-w-0 max-w-full items-center whitespace-normal [overflow-wrap:anywhere] rounded-md border border-border bg-panel-warm px-2 text-[11px] font-medium text-muted"
                >
                    {name}
                </li>
            ))}
        </ul>
    );
}
