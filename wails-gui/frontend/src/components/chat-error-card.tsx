import { memo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { EventsEmit } from '../../wailsjs/runtime/runtime';
import { cn } from '@/lib/utils';
import type { ChatError } from '@/lib/chat-error';

interface ChatErrorCardProps {
    error: ChatError;
    /** The AI the line names ("Claude Code", "Codex", a provider); "your AI" when empty. */
    assistant?: string;
    /** Kept for callers; the error is always one line now. */
    compact?: boolean;
    /** Resends the same message with its dedupe key. Try again shows only when set. */
    onRetry?: () => void;
}

type Tone = 'copper' | 'gray';
type Link = 'set_up' | 'sign_in' | 'see_usage' | 'try_again';

export interface ChatCauseLine {
    line: string;
    tone: Tone;
    link?: Link;
    /** Lines that already name the fix show no Details. */
    details: boolean;
}

/** Set up, Sign in and See usage open Settings › Your AI, as Tasks' Set up does. */
export function openYourAI() {
    EventsEmit('settings:open', 'ai-provider');
}

export function chatCauseLine(error: ChatError, assistant: string | undefined, t: TFunction<'chat'>): ChatCauseLine {
    const named = assistant?.trim();
    const ai = named || t('errors.cause.yourAI');
    const leadAI = named || t('errors.cause.yourAICapital');
    switch (error.kind) {
        case 'no_assistant':
            return { line: t('errors.cause.noAssistantKept'), tone: 'copper', link: 'set_up', details: false };
        case 'signed_out':
            return { line: t('errors.cause.signedOut', { ai }), tone: 'copper', link: 'sign_in', details: false };
        case 'quota_period': {
            const line = error.period === 'daily'
                ? t('errors.cause.usageDaily', { ai: leadAI })
                : error.period === 'monthly'
                    ? t('errors.cause.usageMonthly', { ai: leadAI })
                    : t('errors.cause.usageWeekly', { ai: leadAI });
            return { line, tone: 'copper', link: 'see_usage', details: false };
        }
        case 'quota_generic':
        case 'subscription':
            return { line: t('errors.cause.usageLimit', { ai: leadAI }), tone: 'copper', link: 'see_usage', details: false };
        case 'runner_unavailable':
            return { line: t('errors.cause.runnerUnavailable', { ai }), tone: 'copper', link: 'set_up', details: false };
        case 'network':
            return { line: t('errors.cause.network', { ai }), tone: 'gray', link: 'try_again', details: false };
        case 'server':
            return { line: t('errors.cause.server', { ai: leadAI }), tone: 'gray', link: 'try_again', details: false };
        case 'request_too_large':
            return {
                line: t('errors.line', { title: t('errors.requestTooLarge.title'), body: t('errors.requestTooLarge.body') }),
                tone: 'gray',
                details: false,
            };
        case 'assistant_error':
        case 'unknown':
        default:
            return {
                line: named ? t('errors.cause.assistantError', { ai: named }) : t('errors.cause.assistantErrorUnknown'),
                tone: 'gray',
                link: 'try_again',
                details: true,
            };
    }
}

function linkLabel(link: Link, t: TFunction<'chat'>): string {
    switch (link) {
        case 'set_up': return t('errors.actions.setUp');
        case 'sign_in': return t('errors.actions.signIn');
        case 'see_usage': return t('errors.actions.seeUsage');
        case 'try_again': return t('errors.actions.tryAgain');
    }
}

const LINK_CLASS = 'cursor-pointer font-medium underline-offset-2 hover:underline focus-visible:underline';

/** One line where the answer would go: copper when the person can fix it, gray otherwise. Never red. */
export const ChatErrorCard = memo(function ChatErrorCard({ error, assistant, onRetry }: ChatErrorCardProps) {
    const { t } = useTranslation('chat');
    const [detailOpen, setDetailOpen] = useState(false);
    const cause = chatCauseLine(error, assistant, t);
    const link = cause.link === 'try_again' && !onRetry ? undefined : cause.link;
    const showDetails = cause.details && Boolean(error.detail);
    const onLink = link === 'try_again' ? onRetry : openYourAI;

    return (
        <div className="animate-rise-in max-w-md" data-chat-cause={error.kind}>
            <p role="status" className={cn('m-0 text-xs leading-relaxed', cause.tone === 'copper' ? 'text-warning' : 'text-subtle')}>
                <span>{cause.line}</span>
                {link && (
                    <>
                        <span aria-hidden="true">{t('errors.cause.separator')}</span>
                        <button type="button" className={LINK_CLASS} onClick={onLink}>{linkLabel(link, t)}</button>
                    </>
                )}
                {showDetails && (
                    <>
                        <span aria-hidden="true">{t('errors.cause.separator')}</span>
                        <button type="button" className={LINK_CLASS} aria-expanded={detailOpen} onClick={() => setDetailOpen((open) => !open)}>
                            {detailOpen ? t('errors.actions.hide') : t('errors.actions.details')}
                        </button>
                    </>
                )}
            </p>
            {showDetails && detailOpen && (
                <pre className="m-0 mt-1.5 max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-panel px-2 py-1.5 font-mono text-[11px] leading-relaxed text-muted">
                    {error.detail}
                </pre>
            )}
        </div>
    );
});
