import { createContext, useContext, useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { DigestMarkdown } from './digest-markdown';
import { useMainChatPanel } from '@/hooks/useMainChatPanel';
import type { TodayChatContextRef } from '@/lib/today-chat-navigation';
import { ACTION_LINK } from './activity-inline-notice';

/** Opens the main chat (Open in Chat). Absent where Activity has no chat. */
export const ActivityOpenChatContext = createContext<(() => void) | undefined>(undefined);

/**
 * Chat about this / Ask, in place (activity-review v1.0.0 P1 #10): a focused
 * field under the thing asked about, the answer below it, and Open in Chat.
 * The question goes to the main chat with the item as its context.
 */
export function ActivityAskInline({ context, onClose }: { context?: TodayChatContextRef; onClose: () => void }) {
    const { t } = useTranslation('activity');
    const openChat = useContext(ActivityOpenChatContext);
    const chat = useMainChatPanel(true);
    const [question, setQuestion] = useState('');
    const [asked, setAsked] = useState('');
    const [failed, setFailed] = useState(false);

    const ask = async (words = question) => {
        const text = words.trim();
        if (!text) return;
        setFailed(false);
        setAsked(text);
        const sent = await chat.send(text, context ?? null, true);
        if (sent === 'failed') setFailed(true);
    };
    const keys = (e: KeyboardEvent<HTMLInputElement>) => {
        if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
            e.preventDefault();
            void ask();
        } else if (e.key === 'Escape') {
            e.preventDefault();
            onClose();
        }
    };

    const userIndex = asked && chat.lastUserItemId
        ? chat.items.findIndex((item) => item.id === chat.lastUserItemId && item.kind === 'user')
        : -1;
    const answer = userIndex < 0
        ? undefined
        : chat.items.slice(userIndex + 1).find((item) => item.kind === 'assistant' && !!item.content?.trim());
    const sending = chat.pending?.state === 'sending';
    const answering = !!asked && !failed && !answer && (sending || chat.waiting || userIndex >= 0);
    const sendFailed = failed || (!!asked && chat.pending?.state === 'failed');

    return (
        <div className="space-y-2" data-testid="activity-ask-inline">
            <div className="flex items-center gap-2">
                <input
                    type="text"
                    autoFocus
                    aria-label={t('ask.placeholder')}
                    placeholder={t('ask.placeholder')}
                    className="flex-1 min-w-0 rounded-md border border-border bg-panel px-2.5 py-1.5 text-[13px] text-ink placeholder:text-muted/60 focus:outline-none focus:ring-1 focus:ring-accent"
                    value={question}
                    onChange={(e) => setQuestion(e.target.value)}
                    onKeyDown={keys}
                />
                <Button size="sm" onClick={() => void ask()} disabled={!question.trim() || sending}>
                    {t('ask.send')}
                </Button>
            </div>
            {sendFailed ? (
                <p role="alert" className="text-[12px] text-copper">
                    {t('ask.failed')}{' '}
                    <button type="button" className={ACTION_LINK} onClick={() => void ask(asked)}>
                        {t('ask.tryAgain')}
                    </button>
                </p>
            ) : answer?.content ? (
                <div className="space-y-1.5">
                    <DigestMarkdown text={answer.content} />
                    {openChat && (
                        <button type="button" className="text-[12px] text-muted hover:text-ink transition-colors" onClick={openChat}>
                            {t('ask.openInChat')}
                        </button>
                    )}
                </div>
            ) : answering ? (
                <p className="flex items-center gap-1.5 text-[12px] text-muted">
                    <Loader2 className="h-3 w-3 animate-spin" />
                    {t('ask.answering')}
                </p>
            ) : null}
        </div>
    );
}
