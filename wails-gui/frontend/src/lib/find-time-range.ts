/** A date range Find time reads when the question names a day or a week. */
export type FindTimeRange = { first: string; last: string; minutes: number; nextWeek?: boolean };

const EN_DAYS = ["sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"];
const ZH_DAYS: Record<string, number> = { 日: 0, 天: 0, 一: 1, 二: 2, 三: 3, 四: 4, 五: 5, 六: 6 };

function localDate(day: Date): string {
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${day.getFullYear()}-${pad(day.getMonth() + 1)}-${pad(day.getDate())}`;
}

function addDays(day: Date, days: number): Date {
    return new Date(day.getFullYear(), day.getMonth(), day.getDate() + days, 12);
}

function minutesIn(text: string): number {
    const half = /\bhalf an hour\b|半小时/i.test(text);
    if (half) return 30;
    const hours = /(\d+(?:\.\d+)?)\s*(?:hours?|hrs?|小时|个小时)/i.exec(text);
    if (hours) return Math.min(24 * 60, Math.max(1, Math.round(Number(hours[1]) * 60)));
    const minutes = /(\d+)\s*(?:minutes?|mins?|分钟)/i.exec(text);
    if (minutes) return Math.min(24 * 60, Math.max(1, Number(minutes[1])));
    return 60;
}

/** Reads a named day (today, tomorrow, a weekday) or week (this or next week)
 *  in English or Chinese. Anything else returns null: no range is guessed. */
export function findTimeRangeFor(text: string, now: Date): FindTimeRange | null {
    const words = text.toLowerCase();
    const today = addDays(now, 0);
    const minutes = minutesIn(text);
    const monday = (day: Date) => addDays(day, -((day.getDay() + 6) % 7));
    const nextMonday = addDays(monday(today), 7);
    const zhDay = /(?:周|星期|礼拜)([一二三四五六日天])/.exec(text);
    const enDay = EN_DAYS.findIndex((name) => new RegExp(`\\b${name}\\b`).test(words));
    const nextWeek = /\bnext week\b|下周|下个?星期|下个?礼拜/.test(words);
    const weekday = zhDay ? ZH_DAYS[zhDay[1]] : enDay;
    if (weekday >= 0) {
        const nextNamed = nextWeek || /\bnext\s+(?:sunday|monday|tuesday|wednesday|thursday|friday|saturday)\b/.test(words);
        const day = nextNamed ? addDays(nextMonday, (weekday + 6) % 7) : addDays(today, (weekday - today.getDay() + 7) % 7);
        return { first: localDate(day), last: localDate(day), minutes };
    }
    if (nextWeek) return { first: localDate(nextMonday), last: localDate(addDays(nextMonday, 4)), minutes, nextWeek: true };
    if (/\bthis week\b|这周|本周|这个?星期|这个?礼拜/.test(words)) {
        const friday = addDays(monday(today), 4);
        return { first: localDate(today), last: localDate(friday < today ? today : friday), minutes };
    }
    if (/\btomorrow\b|明天/.test(words)) {
        const day = addDays(today, 1);
        return { first: localDate(day), last: localDate(day), minutes };
    }
    if (/\btoday\b|今天/.test(words)) return { first: localDate(today), last: localDate(today), minutes };
    return null;
}
