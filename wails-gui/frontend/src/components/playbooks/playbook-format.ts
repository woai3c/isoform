import type { PlaybookView } from '@/lib/wails-shim-app';

/** How a playbook runs, as the card says it: on its schedule, or only when asked. */
export type PlaybookRunWhen =
    | { kind: 'weekdays'; time: string }
    | { kind: 'daily'; time: string }
    | { kind: 'onRequest' };

/** A schedule that is switched on runs by itself; anything else runs when asked. */
export function playbookRunWhen(plan: PlaybookView | null | undefined): PlaybookRunWhen {
    const schedule = plan?.schedule;
    if (!schedule || !schedule.enabled || !schedule.time) return { kind: 'onRequest' };
    return schedule.weekdays
        ? { kind: 'weekdays', time: schedule.time }
        : { kind: 'daily', time: schedule.time };
}

/** "08:55" to a clock time in the locale, such as "8:55 AM". Returns the input when it is not HH:MM. */
export function formatClock(time: string, locale: string): string {
    const match = /^(\d{1,2}):(\d{2})$/.exec(time.trim());
    if (!match) return time;
    const hours = Number(match[1]);
    const minutes = Number(match[2]);
    if (hours > 23 || minutes > 59) return time;
    return new Date(2000, 0, 1, hours, minutes).toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' });
}

const DAY_MS = 24 * 60 * 60 * 1000;

function startOfDay(ms: number): number {
    const day = new Date(ms);
    day.setHours(0, 0, 0, 0);
    return day.getTime();
}

/**
 * The day of a past run: "today", "yesterday", a short weekday within the
 * last week ("Mon"), otherwise a date ("Oct 21"). Returns `key` for the two
 * words that need translating; otherwise `text` is ready to show.
 */
export function formatRunDay(at: number, now: number, locale: string): { key: 'today' | 'yesterday' | null; text: string } {
    const today = startOfDay(now);
    const day = startOfDay(at);
    if (day >= today) return { key: 'today', text: '' };
    if (day >= today - DAY_MS) return { key: 'yesterday', text: '' };
    if (today - day < 7 * DAY_MS) {
        return { key: null, text: new Date(at).toLocaleDateString(locale, { weekday: 'short' }) };
    }
    return { key: null, text: new Date(at).toLocaleDateString(locale, { month: 'short', day: 'numeric' }) };
}
