// Plain-words formatting for the Who you know page: loops, times, app names and the
// prepared requests. Every word comes from the `people` namespace; this file
// only picks the key and fills it.
import type { ContactsAccessStatus, PersonLoop } from '@/lib/wails-shim-app';
import { computerDayClock } from '@/lib/brief-time';

export type Translate = (key: string, options?: Record<string, unknown>) => string;

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

// The editor offers a fixed set of relationships, each with a label. A value
// it does not know (saved by another surface or an older build) is the
// person's own data, so it shows as stored.
export function relationshipLabel(
    translate: (key: string) => string,
    exists: (key: string) => boolean,
    relationship: string,
): string {
    const key = `people:relationship.${relationship}`;
    return exists(key) ? translate(key) : relationship;
}

// Shorten a raw account ID (e.g. a Slack user ID or WeChat open ID) to a
// prefix…suffix form, used when an app handle has no human-readable display name.
export function maskHandleId(id: string): string {
    const value = id.trim();
    if (value.length <= 12) return value;
    return `${value.slice(0, 4)}…${value.slice(-6)}`;
}

// Product names stay as written; the ids that are plain words (email,
// calendar) are translated. Anything else shows as stored, capitalized.
const APP_NAMES: Record<string, string> = {
    slack: 'Slack',
    wechat: 'WeChat',
    weixin: 'WeChat',
    telegram: 'Telegram',
    discord: 'Discord',
    feishu: 'Feishu',
    lark: 'Lark',
    imessage: 'iMessage',
    whatsapp: 'WhatsApp',
    zoom: 'Zoom',
    teams: 'Teams',
};
const APP_WORDS: Record<string, string> = {
    email: 'apps.email',
    mail: 'apps.email',
    calendar: 'apps.calendar',
    contacts: 'apps.contacts',
};

export function appLabel(t: Translate, app: string): string {
    const id = app.trim().toLowerCase();
    if (APP_WORDS[id]) return t(APP_WORDS[id]);
    if (APP_NAMES[id]) return APP_NAMES[id];
    const value = app.trim();
    return value ? value.charAt(0).toUpperCase() + value.slice(1) : value;
}

type TogetherItem = { kind: string; title: string; app: string; durationMs: number; direction?: string };

const CJK = /[\u3400-\u9fff]/;
// An Activity title that opens on a past-tense verb ("Messaged Priya…",
// "Wrote the notes…") gets its subject: you.
const PAST_VERB =
    /^(?:[a-z]{2,}(?<!e)ed|wrote|sent|met|read|made|took|spoke|had|saw|went|gave|built|ran|drew|told|got|did|paid|left|found|kept|brought|bought|thought|caught|sold|held|led|set|put|began|chose|heard|knew|said|sat|spent|stood|won|wore)\b/i;

function youDid(t: Translate, title: string): string {
    const value = title.trim();
    if (CJK.test(value)) {
        return /^[你我]/.test(value) || !value.includes('了') ? value : t('together.youDid', { what: value, lng: 'zh-CN' });
    }
    if (/^you\b/i.test(value) || !PAST_VERB.test(value)) return value;
    return t('together.youDid', { what: value.charAt(0).toLowerCase() + value.slice(1), lng: 'en' });
}

function shortName(name: string): string {
    const value = name.trim();
    return CJK.test(value) ? value : (value.split(/\s+/)[0] ?? value);
}

/** One Together line that says who did it, the app after the dot only when the verb doesn't name it (N39, N41). */
export function togetherWords(t: Translate, item: TogetherItem, name: string): string {
    if (item.kind === 'meeting') {
        const title = item.title || t('together.meetingUntitled');
        return item.durationMs > 0 ? t('together.withDuration', { title, duration: durationWords(t, item.durationMs) }) : title;
    }
    const app = item.app ? appLabel(t, item.app) : '';
    const withApp = (words: string) => (app ? t('together.activity', { title: words, app }) : words);
    if (item.kind === 'activity') return withApp(youDid(t, item.title));
    const who = item.direction === 'in' ? 'fromThem' : item.direction === 'out' ? 'fromYou' : 'with';
    const person = shortName(name);
    if (APP_WORDS[item.app.trim().toLowerCase()] === 'apps.email') return t(`together.${who}Email`, { name: person });
    return withApp(t(`together.${who}Message`, { name: person }));
}

export function listWords(lang: string, items: string[]): string {
    if (items.length === 0) return '';
    try {
        return new Intl.ListFormat(lang, { style: 'long', type: 'conjunction' }).format(items);
    } catch {
        return items.join(', ');
    }
}

function startOfDay(ms: number): number {
    const d = new Date(ms);
    d.setHours(0, 0, 0, 0);
    return d.getTime();
}

/** Calendar days from now to ms: 0 today, -1 yesterday, 1 tomorrow. */
function dayOffset(ms: number, now: number): number {
    return Math.round((startOfDay(ms) - startOfDay(now)) / DAY);
}

function dateWords(lang: string, ms: number, now: number): string {
    const sameYear = new Date(ms).getFullYear() === new Date(now).getFullYear();
    return new Date(ms).toLocaleDateString(lang, sameYear ? { month: 'short', day: 'numeric' } : { year: 'numeric', month: 'short', day: 'numeric' });
}

/** "Today", "Yesterday", "Oct 14": the day something happened. */
export function dayWords(t: Translate, lang: string, ms: number, now = Date.now()): string {
    const offset = dayOffset(ms, now);
    if (offset === 0) return t('time.today');
    if (offset === -1) return t('time.yesterday');
    if (offset === 1) return t('time.tomorrow');
    return dateWords(lang, ms, now);
}

/** "11:20 PM" today, "yesterday", "Oct 18" before: when someone was last seen. */
export function lastSeenWords(t: Translate, lang: string, ms: number, now = Date.now()): string {
    const offset = dayOffset(ms, now);
    if (offset === 0) return new Date(ms).toLocaleTimeString(lang, { hour: 'numeric', minute: '2-digit' });
    if (offset === -1) return t('time.yesterdayLower');
    return dateWords(lang, ms, now);
}

/** "40 min", "9 h", "3 d": how long something has waited. */
export function ageWords(t: Translate, ms: number, now = Date.now()): string {
    const delta = Math.max(0, now - ms);
    if (delta < HOUR) return t('time.minutes', { count: Math.max(1, Math.floor(delta / 60_000)) });
    if (delta < DAY) return t('time.hours', { count: Math.floor(delta / HOUR) });
    return t('time.days', { count: Math.floor(delta / DAY) });
}

export function durationWords(t: Translate, ms: number): string {
    const minutes = Math.round(ms / 60_000);
    if (minutes < 60) return t('time.durationMinutes', { count: Math.max(1, minutes) });
    if (minutes >= 24 * 60) return t('time.durationDays', { count: Math.round(minutes / (24 * 60)) });
    const hours = Math.floor(minutes / 60);
    const rest = minutes % 60;
    return rest > 0 ? t('time.durationHoursMinutes', { hours, minutes: rest }) : t('time.durationHours', { count: hours });
}

/** Replies and things you owe ask for action; the rest are quiet. */
export function loopNeedsYou(loop: PersonLoop): boolean {
    return loop.kind === 'reply_owed' || loop.kind === 'you_owe';
}

/** The short chip on a card: what kind of loop and how old or when due. */
export function loopChip(t: Translate, lang: string, loop: PersonLoop, now = Date.now()): string {
    switch (loop.kind) {
        case 'reply_owed':
            return t('loop.replyOwed', { age: ageWords(t, loop.since, now) });
        case 'you_owe':
            return loop.dueAt > 0 ? t('loop.youOweDue', { when: dayWords(t, lang, loop.dueAt, now) }) : t('loop.youOwe');
        case 'waiting_on_them':
            return loop.dueAt > 0 ? t('loop.waitingOnThemDue', { when: dayWords(t, lang, loop.dueAt, now) }) : t('loop.waitingOnThem');
        default:
            return t('loop.reappeared');
    }
}

/** One loop as a sentence, for the card's context line and the detail list. */
export function loopLine(t: Translate, loop: PersonLoop, now = Date.now()): string {
    switch (loop.kind) {
        case 'reply_owed':
            return t('loopLine.replyOwed', { age: ageWords(t, loop.since, now) });
        case 'you_owe':
            return loop.title || t('loop.youOwe');
        case 'waiting_on_them':
            return loop.title ? t('loopLine.waitingOnThem', { title: loop.title }) : t('loop.waitingOnThem');
        default:
            return t('loopLine.reappeared');
    }
}

export interface PeopleHeadlineCounts {
    repliesOwed: number;
    peopleWaitingOnYou: number;
}

/** The page's headline says what you owe, in one sentence. */
export function headlineWords(t: Translate, counts: PeopleHeadlineCounts, openCount: number, paused: boolean): string {
    if (paused) return t('overview.headlinePaused');
    const replies = counts.repliesOwed > 0 ? t('overview.headlineReplies', { count: counts.repliesOwed }) : '';
    const waiting = counts.peopleWaitingOnYou > 0 ? t('overview.headlineWaiting', { count: counts.peopleWaitingOnYou }) : '';
    if (replies && waiting) return t('overview.headlineBoth', { replies, waiting });
    if (replies) return replies;
    if (waiting) return t('overview.headlineWaitingOnly', { count: counts.peopleWaitingOnYou });
    return openCount > 0 ? t('overview.headlineNothingOwed') : t('overview.headlineNothingOpen');
}

/** The words Brief me and Draft a reply put in the main chat's box. The
 *  chat adds what Isoform knows about a person it finds named in a message,
 *  so the name is the one stored on the profile. */
/** What Contacts' line on the page says. Waiting for the first pass says
 *  nothing; an action (Connect, Allow access, Try again, Stop) rides along. */
export type ContactsLine =
    | { kind: 'none' }
    | { kind: 'text'; text: string }
    | { kind: 'connect' }
    | { kind: 'resume'; text: string }
    | { kind: 'access'; text: string }
    | { kind: 'syncing'; text: string }
    | { kind: 'failed'; text: string };

export function contactsLine(access: ContactsAccessStatus, translate: (key: string, args?: Record<string, string>) => string, language: string): ContactsLine {
    if (access.supported === false) return { kind: 'none' };
    if (!access.consentAccepted) return { kind: 'connect' };
    if (access.syncPaused) return { kind: 'resume', text: translate('people:contactsSyncPaused') };
    if (access.permission !== 'granted') return { kind: 'access', text: translate('people:contactsSyncNeedsAccess') };
    if (access.syncState === 'syncing') return { kind: 'syncing', text: translate('people:contactsSyncing') };
    if (access.syncState === 'error') return { kind: 'failed', text: translate('people:contactsSyncError') };
    const synced = contactsSyncedWords(access, translate, language);
    return synced ? { kind: 'text', text: synced } : { kind: 'none' };
}

export function contactsSyncedWords(access: ContactsAccessStatus, translate: (key: string, args?: Record<string, string>) => string, language: string): string {
    if (!access.lastImportAt) return '';
    const last = new Date(access.lastImportAt);
    if (!Number.isFinite(last.getTime())) return '';
    const now = new Date();
    if (last.toDateString() === now.toDateString()) return translate(last.getHours() < 12 ? 'people:contactsSyncedMorning' : 'people:contactsSyncedToday');
    const date = last.toLocaleDateString(language, { month: 'short', day: 'numeric', ...(last.getFullYear() === now.getFullYear() ? {} : { year: 'numeric' }) });
    return translate('people:contactsSyncedDate', { date });
}

/** "1:05 PM", "Yesterday, 7:01 PM", "Thu, 10:58 PM": the computer's clock. */
export function formatAbsolute(lang: string, ms: number): string {
    if (!ms) return '';
    return computerDayClock(new Date(ms), lang);
}

/** People reads refuse to publish when the data changed while they ran
 *  (ErrReadRevisionChanged); one quiet second try covers that race. */
export async function readWithRetry<T>(read: () => Promise<T>): Promise<T> {
    try {
        return await read();
    } catch {
        await new Promise((resolve) => setTimeout(resolve, 120));
        return read();
    }
}
