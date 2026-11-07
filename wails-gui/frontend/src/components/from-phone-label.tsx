import { useTranslation } from 'react-i18next';
import type { StreamItem } from '@/types';

// A user message sent from the phone carries origin "phone" in its ToolInput (mobileChatPhoneOriginToolInput).
export function isFromPhone(item: Pick<StreamItem, 'toolInput'>): boolean {
    const input = item.toolInput;
    return typeof input === 'object' && input !== null && (input as { origin?: unknown }).origin === 'phone';
}

// The small caption above a phone message in the computer's Chat (#635).
export function FromPhoneLabel() {
    const { t } = useTranslation('chat');
    return <span className="text-xs text-muted">{t('fromPhone')}</span>;
}
