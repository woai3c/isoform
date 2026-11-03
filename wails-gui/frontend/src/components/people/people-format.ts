// Plain-words formatting for the Who you know page: loops, times, app names and the
// prepared requests. Every word comes from the `people` namespace; this file
// only picks the key and fills it.
import type { ContactsAccessStatus, PersonLoop } from '@/lib/wails-shim-app';

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
    const hours = Math.round((minutes / 60) * 10) / 10;
    return t('time.durationHours', { count: hours });
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
export function preparedRequest(t: Translate, kind: 'brief' | 'draft', name: string): string {
    return kind === 'brief' ? t('prompts.brief', { name }) : t('prompts.draft', { name });
}

export function contactsStatusText(access: ContactsAccessStatus, translate: (key: string, args?: Record<string, string>) => string, language: string): string {
    if (access.supported === false) return translate('people:contactsImportUnavailable');
    if (!access.consentAccepted) return translate('people:contactsSyncConsent');
    if (access.syncPaused) return translate('people:contactsSyncPaused');
    if (access.permission !== 'granted') return translate('people:contactsSyncNeedsAccess');
    if (access.syncState === 'syncing') return translate('people:contactsSyncing');
    if (access.syncState === 'error') return translate('people:contactsSyncError');
    if (!access.lastImportAt) return translate('people:contactsSyncWaiting');
    const last = new Date(access.lastImportAt);
    if (!Number.isFinite(last.getTime())) return translate('people:contactsSyncWaiting');
    const now = new Date();
    if (last.toDateString() === now.toDateString()) return translate(last.getHours() < 12 ? 'people:contactsSyncedMorning' : 'people:contactsSyncedToday');
    return translate('people:contactsSyncedDate', { date: last.toLocaleDateString(language) });
}

export function formatAbsolute(lang: string, ms: number): string {
    if (!ms) return '';
    const d = new Date(ms);
    if (d.getFullYear() === new Date().getFullYear()) {
        return d.toLocaleString(lang, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
    }
    return d.toLocaleDateString(lang, { year: 'numeric', month: 'short', day: 'numeric' });
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
