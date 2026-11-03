import { useTranslation } from 'react-i18next';
import { openYourAI } from './chat-error-card';

/** Above the box while no AI is configured, before anything is typed. Send stays on. */
export function NoAILine() {
    const { t } = useTranslation('chat');
    return (
        <p role="status" className="m-0 mb-1.5 px-1 text-xs text-warning" data-testid="no-ai-line">
            <span>{t('errors.cause.noAssistantBox')}</span>
            <span aria-hidden="true">{t('errors.cause.separator')}</span>
            <button type="button" className="cursor-pointer font-medium underline-offset-2 hover:underline focus-visible:underline" onClick={openYourAI}>
                {t('errors.actions.setUp')}
            </button>
        </p>
    );
}
