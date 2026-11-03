import { useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

export const CHAT_FAILURE_LINK_CLASS = 'cursor-pointer font-medium underline-offset-2 hover:underline focus-visible:underline';

/**
 * One set of failure words (Chat K3): the line sits where the thing failed,
 * gray or copper, never red, and never a toast. A person's Try again that
 * fails too turns the words into "Still didn't work."
 *
 * onRetry resolves false (or throws) when the retry failed; the parent
 * removes the line when it worked.
 */
export function ChatFailureLine({
    text,
    onRetry,
    tone = 'gray',
    className,
    after,
    testId,
}: {
    text: string;
    onRetry?: () => unknown;
    tone?: 'gray' | 'copper';
    className?: string;
    after?: ReactNode;
    testId?: string;
}) {
    const { t } = useTranslation('chat');
    const [still, setStill] = useState(false);
    const busy = useRef(false);
    const retry = onRetry ? async () => {
        if (busy.current) return;
        busy.current = true;
        let ok = true;
        try {
            ok = (await onRetry()) !== false;
        } catch {
            ok = false;
        } finally {
            busy.current = false;
        }
        if (!ok) setStill(true);
    } : undefined;
    return (
        <p
            role="alert"
            data-chat-failure={testId ?? ''}
            data-still={still || undefined}
            className={cn('m-0 text-[12.5px]', tone === 'copper' ? 'text-warning' : 'text-subtle', className)}
        >
            <span>{still ? t('failure.still') : text}</span>
            {retry && !still && (
                <button type="button" className={cn(CHAT_FAILURE_LINK_CLASS, 'ms-1')} onClick={() => void retry()}>
                    {t('errors.actions.tryAgain')}
                </button>
            )}
            {after}
        </p>
    );
}
