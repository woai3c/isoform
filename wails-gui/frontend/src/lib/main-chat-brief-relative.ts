import type { MobileBriefDocument, MobileBriefParagraph } from '@/lib/wails-shim-app';

type Translate = (key: string, options?: Record<string, unknown>) => string;

function civilDay(date: Date, timezone?: string): number | null {
    if (!Number.isFinite(date.getTime())) return null;
    try {
        const parts = new Intl.DateTimeFormat('en-US', { timeZone: timezone || undefined, year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(date);
        const number = (type: string) => Number(parts.find((part) => part.type === type)?.value);
        const value = Date.UTC(number('year'), number('month') - 1, number('day')) / 86400000;
        return Number.isFinite(value) ? value : null;
    } catch { return null; }
}

/** Re-express a brief's relative words against the day it is read. The stored
 *  brief is untouched. A single referenced due date supplies a stronger anchor. */
export function mainChatBriefRelativeText(brief: MobileBriefDocument, paragraph: MobileBriefParagraph, now: Date, language: string, t: Translate): string {
    const base = civilDay(new Date(brief.asOf), brief.timezone);
    const read = civilDay(now, brief.timezone);
    if (base === null || read === null || base === read) return paragraph.text;
    const refs = paragraph.refs ?? [];
    const dueDays = new Set<number>();
    const relativeWords = paragraph.text.match(/\b(?:today|tomorrow|yesterday)\b|今天|明天|昨天/gi) ?? [];
    if (refs.length === 1 && relativeWords.length === 1 && /\bdue\b|到期/i.test(paragraph.text)) {
        for (const items of Object.values(brief.sections ?? {})) {
            if (!Array.isArray(items)) continue;
            for (const item of items) {
                if (!item?.dueAt || !item.refs?.includes(refs[0])) continue;
                const day = civilDay(new Date(item.dueAt), brief.timezone);
                if (day !== null) dueDays.add(day);
            }
        }
    }
    const anchoredDue = dueDays.size === 1 ? [...dueDays][0] : null;
    const name = (target: number) => {
        const diff = target - read;
        if (diff >= -1 && diff <= 1) return t(`mainChat.relativeDay.${diff === 0 ? 'today' : diff === 1 ? 'tomorrow' : 'yesterday'}`);
        const date = new Date(target * 86400000);
        return new Intl.DateTimeFormat(language, Math.abs(diff) < 7
            ? { weekday: 'long', timeZone: 'UTC' }
            : { month: 'short', day: 'numeric', timeZone: 'UTC' }).format(date);
    };
    return paragraph.text.replace(/\bis due (today|tomorrow|yesterday)\b|\b(today|tomorrow|yesterday)\b|今天|明天|昨天/gi, (match, dueWord: string | undefined, relativeWord: string | undefined) => {
        const word = dueWord ?? relativeWord ?? match;
        const lower = word.toLowerCase();
        const offset = lower === 'tomorrow' || word === '明天' ? 1 : lower === 'yesterday' || word === '昨天' ? -1 : 0;
        const target = anchoredDue ?? base + offset;
        const result = name(target);
        if (dueWord) return anchoredDue !== null && target < read ? t('mainChat.relativeDay.wasDue', { day: result }) : match.replace(dueWord, result);
        return /^[A-Z]/.test(word) ? result.charAt(0).toLocaleUpperCase(language) + result.slice(1) : result;
    });
}
