import type { TFunction } from 'i18next';
import type { AboutYouBelief } from '@/lib/wails-shim-app';
import { memoryApps, memoryDate } from '@/lib/memory-content';

export const MEMORY_AREAS = ['communication', 'work', 'people', 'tools', 'routines', 'interests', 'other'] as const;
export type MemoryArea = typeof MEMORY_AREAS[number];

export function memoryArea(area?: string): MemoryArea {
    return MEMORY_AREAS.includes(area as MemoryArea) ? area as MemoryArea : 'other';
}

export interface CommunicationDraftMetadata {
    beliefIds: string[];
    recipientName: string;
    language: string;
    sentences: number;
    greeting: boolean;
}

export function sentMessageEvidence(
    t: TFunction<'memory'>,
    belief: AboutYouBelief | undefined,
    apps: string[],
    locale: string,
): string {
    const count = belief?.sentMessageCount;
    if (typeof count !== 'number' || !Number.isFinite(count) || count <= 0) return '';
    let evidence = t('aboutYou.communication.sentMessages', { count });
    const first = belief?.evidenceFirstAt;
    const last = belief?.evidenceLastAt;
    const validFirst = typeof first === 'number' && Number.isFinite(first) && first > 0;
    const validLast = typeof last === 'number' && Number.isFinite(last) && last > 0;
    if (validFirst && validLast) {
        const firstDate = new Date(first);
        const lastDate = new Date(last);
        const dates = firstDate.toDateString() === lastDate.toDateString()
            ? t('aboutYou.communication.dateSingle', { date: memoryDate(t, first, locale) })
            : t('aboutYou.communication.dateRange', { first: memoryDate(t, first, locale), last: memoryDate(t, last, locale) });
        evidence = t('aboutYou.communication.withDates', { evidence, dates });
    } else if (validFirst) {
        evidence = t('aboutYou.communication.withDates', {
            evidence,
            dates: t('aboutYou.communication.dateFrom', { date: memoryDate(t, first, locale) }),
        });
    } else if (validLast) {
        evidence = t('aboutYou.communication.withDates', {
            evidence,
            dates: t('aboutYou.communication.dateUntil', { date: memoryDate(t, last, locale) }),
        });
    }
    const appNames = memoryApps(apps);
    return appNames.length
        ? t('aboutYou.communication.withApps', {
            evidence,
            apps: new Intl.ListFormat(locale, { style: 'long', type: 'conjunction' }).format(appNames),
        })
        : evidence;
}

export function communicationDraftDetails(
    t: TFunction<any>,
    prefix: 'aboutYou.communication' | 'mainChat.proactive.communication',
    communication: Pick<CommunicationDraftMetadata, 'language' | 'sentences' | 'greeting'>,
): string {
    const normalized = communication.language.trim().toLowerCase();
    const knownLanguage = ['zh', 'zh-cn', 'chinese', '中文'].includes(normalized)
        ? 'chinese'
        : ['en', 'en-us', 'english'].includes(normalized) ? 'english' : null;
    const language = knownLanguage
        ? t(`${prefix}.languages.${knownLanguage}` as never) as string
        : communication.language;
    return String(t(`${prefix}.details` as never, {
        language,
        sentences: t(`${prefix}.sentences` as never, { count: communication.sentences } as never),
        greeting: t(`${prefix}.${communication.greeting ? 'greeting' : 'noGreeting'}` as never),
    } as never));
}
