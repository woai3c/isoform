import type { MobileBriefDocument, MobileBriefParagraph } from '@/lib/wails-shim-app';
import { computerClock } from '@/lib/brief-time';

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

/** A civil day (days since the epoch) named from the day it is read:
 *  today, tomorrow or yesterday, a weekday within a week, else a date. */
function relativeDayName(target: number, read: number, language: string, t: Translate): string {
    const diff = target - read;
    if (diff >= -1 && diff <= 1) return t(`mainChat.relativeDay.${diff === 0 ? 'today' : diff === 1 ? 'tomorrow' : 'yesterday'}`);
    const date = new Date(target * 86400000);
    return new Intl.DateTimeFormat(language, Math.abs(diff) < 7
        ? { weekday: 'long', timeZone: 'UTC' }
        : { month: 'short', day: 'numeric', timeZone: 'UTC' }).format(date);
}

function dayKeyNumber(key: string | undefined): number | null {
    const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(key ?? '');
    if (!match) return null;
    const value = Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3])) / 86400000;
    return Number.isFinite(value) ? value : null;
}

const ENGLISH_WEEKDAYS = ['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday'];
const CHINESE_WEEKDAYS = '日一二三四五六';

/** Re-express the day words of a fold line from a past day against the day
 *  it is read (Thursday polish C2): the words were written on `lineDay`, so
 *  "PT is due Thursday." under Thu, Oct 29 reads "PT was due Thursday." on
 *  Monday. A line about a past day never says "is due", "today" or
 *  "tomorrow". Lines from today, or with no known day, are unchanged. */
export function mainChatPastLineText(text: string, lineDay: string | undefined, today: string, language: string, t: Translate): string {
    const base = dayKeyNumber(lineDay);
    const read = dayKeyNumber(today);
    if (base === null || read === null || base >= read) return text;
    const weekdayOnOrAfter = (weekday: number) => {
        const baseWeekday = new Date(base * 86400000).getUTCDay();
        return base + ((weekday - baseWeekday + 7) % 7);
    };
    const englishTarget = (word: string) => {
        const lower = word.toLowerCase();
        if (lower === 'today') return base;
        if (lower === 'tomorrow') return base + 1;
        if (lower === 'yesterday') return base - 1;
        return weekdayOnOrAfter(ENGLISH_WEEKDAYS.indexOf(lower));
    };
    const name = (target: number) => relativeDayName(target, read, language, t);
    const capital = (word: string, result: string) => /^[A-Z]/.test(word) ? result.charAt(0).toLocaleUpperCase(language) + result.slice(1) : result;
    const english = text.replace(
        /\b(is|are) due (?:on )?(today|tomorrow|yesterday|sunday|monday|tuesday|wednesday|thursday|friday|saturday)\b|\b(today|tomorrow|yesterday)\b/gi,
        (match, verb: string | undefined, dueWord: string | undefined, relativeWord: string | undefined) => {
            if (verb && dueWord) {
                const target = englishTarget(dueWord);
                if (target < read) return t(verb.toLowerCase() === 'are' ? 'mainChat.relativeDay.lineWereDue' : 'mainChat.relativeDay.lineWasDue', { day: name(target) });
                return `${verb} due ${name(target)}`;
            }
            const word = relativeWord ?? match;
            return capital(word, name(englishTarget(word)));
        },
    );
    return english.replace(/(今天|明天|昨天|(?:周|星期)[日天一二三四五六])(到期)?/g, (_match, word: string, due: string | undefined) => {
        const target = word === '今天' ? base : word === '明天' ? base + 1 : word === '昨天' ? base - 1
            : weekdayOnOrAfter(CHINESE_WEEKDAYS.indexOf(word.slice(-1) === '天' ? '日' : word.slice(-1)));
        if (due && target < read) return t('mainChat.relativeDay.lineWasDue', { day: name(target) });
        return name(target) + (due ?? '');
    });
}

/** A missed brief's line, worded from the brief's own day against the day it
 *  is read (chat-review v1.0.10 H9): "Last night's evening brief didn't come
 *  at 6:00 PM." A recorded cause uses Today's words. Null when the line does
 *  not carry the brief's day, slot and due time. */
export function mainChatBriefMissedText(
    entry: { briefDay?: string; briefSlot?: string; briefTime?: string; briefCause?: string },
    today: string,
    language: string,
    t: Translate,
    tToday: Translate,
): string | null {
    const base = dayKeyNumber(entry.briefDay);
    const read = dayKeyNumber(today);
    const due = /^(\d{1,2}):(\d{2})$/.exec(entry.briefTime ?? '');
    if (base === null || read === null || base > read || !due || (entry.briefSlot !== 'morning' && entry.briefSlot !== 'evening')) return null;
    const evening = entry.briefSlot === 'evening';
    const diff = read - base;
    const when = diff === 0 ? t(evening ? 'mainChat.briefMissed.when.thisEvening' : 'mainChat.briefMissed.when.thisMorning')
        : diff === 1 ? t(evening ? 'mainChat.briefMissed.when.lastNight' : 'mainChat.briefMissed.when.yesterday')
        : t(diff < 7 ? 'mainChat.briefMissed.when.weekday' : 'mainChat.briefMissed.when.date', { day: relativeDayName(base, read, language, t) });
    const [year, month, day] = (entry.briefDay ?? '').split('-').map(Number);
    const time = computerClock(new Date(year, month - 1, day, Number(due[1]), Number(due[2])), language);
    const cause = entry.briefCause === 'asleep' || entry.briefCause === 'closed' ? tToday(`todayBrief.lateReason.${entry.briefCause}`)
        : entry.briefCause === 'failed' ? t('mainChat.briefMissed.cause.failed') : '';
    return t(cause ? 'mainChat.briefMissed.lineCause' : 'mainChat.briefMissed.line', { when, slot: t(`mainChat.briefMissed.slot.${entry.briefSlot}`), time, cause });
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
    const name = (target: number) => relativeDayName(target, read, language, t);
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
