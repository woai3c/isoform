import { useTranslation } from 'react-i18next';

/** A result line in place of a toast (activity-review v1.0.0 P1 #11): what
 *  happened, with Undo after a change and Try again after a failure. */
export interface ActivityNotice {
    tone: 'done' | 'error';
    text: string;
    actionLabel?: string;
    onAction?: () => void;
}

/** Failure lines are copper and their Try again an accent link (activity
 *  review v1.0.3 #6). */
export const ACTION_LINK = 'text-accent hover:underline';

export function errorNotice(text: string, retry?: () => void): ActivityNotice {
    return { tone: 'error', text, onAction: retry };
}

export function ActivityInlineNotice({ notice, className = '' }: { notice: ActivityNotice | null; className?: string }) {
    const { t } = useTranslation('activity');
    if (!notice) return null;
    const label = notice.actionLabel ?? (notice.tone === 'error' && notice.onAction ? t('ask.tryAgain') : undefined);
    return (
        <p
            role={notice.tone === 'error' ? 'alert' : 'status'}
            className={`text-[12px] ${notice.tone === 'error' ? 'text-copper' : 'text-muted'} ${className}`}
        >
            {notice.text}
            {label && notice.onAction && (
                <>
                    {' '}
                    <button type="button" className={ACTION_LINK} onClick={notice.onAction}>
                        {label}
                    </button>
                </>
            )}
        </p>
    );
}
