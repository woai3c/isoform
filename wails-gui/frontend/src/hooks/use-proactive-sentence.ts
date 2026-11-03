import { useTranslation } from 'react-i18next';
import type { MainChatProactivePayload } from '@/lib/stream-helpers';

/** Display only the gated card projection. Evidence IDs cannot authorize a read. */
export function useProactiveSentence(message: MainChatProactivePayload): string {
    const { i18n } = useTranslation('chat');
    const copy = message.localized?.[i18n.resolvedLanguage?.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'] ?? message;
    const supplied = copy.body.trim() !== copy.headline.trim() ? copy.body.trim() : '';
    const delivered = message.body.trim() !== message.headline.trim() ? message.body.trim() : '';
    const workSummary = message.work?.summary?.trim();
    return supplied || delivered || (workSummary !== copy.headline.trim() ? workSummary : '') || message.draftContent?.subject?.trim() || message.permission?.details?.trim() || '';
}
