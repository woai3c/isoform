import i18n from "@/i18n";
/** Memoized time labels depend on the local day and time zone, not each tick. */
export function clockContextKey(now = new Date()): string {
    return `${now.toDateString()}:${new Intl.DateTimeFormat().resolvedOptions().timeZone}`;
}

/** Names share Today's clock and slot translations, including older requests. */
export function briefSlotKey(slot: string | undefined, asOf?: string, timeZone?: string): "morning" | "evening" {
    if (slot === "morning" || slot === "evening") return slot;
    const at = asOf ? new Date(asOf) : null;
    if (!at || Number.isNaN(at.getTime())) return "morning";
    let hour: number;
    try {
        hour = Number(new Intl.DateTimeFormat("en-GB", { hour: "numeric", hourCycle: "h23", timeZone }).format(at));
    } catch {
        hour = at.getHours();
    }
    return hour < 7 || hour >= 18 ? "evening" : "morning";
}

type ClockKey = "todayBrief.clock.time" | "todayBrief.clock.dateAtTime"
    | `todayBrief.clock.period.${"early" | "morning" | "noon" | "afternoon" | "evening"}`;
type Translate = (key: ClockKey, options?: Record<string, unknown>) => string;

let computerUses24Hour = false;

/** The computer's own clock setting (Go GetClockFormat). Every time on Chat,
 *  Today and Tasks follows it (chat-review v1.0.2 #4). */
export function setComputerClock(format: string): void {
    computerUses24Hour = format === "24h";
}

/** The one clock on Today (Claude Design, 2026-10-22): "7:00 AM", "12:30 PM",
 *  "2:47 PM" in English, "上午7:00", "中午12:30", "下午2:47" in Chinese, or
 *  "14:47" when the computer uses 24-hour time. */
function shortClock(hour: number, minute: number, language: string, t: Translate): string {
    if (computerUses24Hour) return `${String(hour).padStart(2, "0")}:${String(minute).padStart(2, "0")}`;
    const h = (hour + 11) % 12 + 1;
    const chinese = language.startsWith("zh");
    const periodKey = chinese
        ? hour < 6 ? "early" : hour < 12 ? "morning" : hour === 12 ? "noon" : hour <= 18 ? "afternoon" : "evening"
        : hour < 12 ? "morning" : "afternoon";
    const period = t(`todayBrief.clock.period.${periodKey}`);
    return t("todayBrief.clock.time", { period, hour: h, minutes: String(minute).padStart(2, "0") });
}

/** A local time of day in the one clock Today uses. */
export function clockTime(when: Date, language: string, t: Translate): string {
    return shortClock(when.getHours(), when.getMinutes(), language, t);
}

/** Compact unambiguous clocks in prose. Bare colon numbers may be aspect ratios
 *  or media offsets; format known clock metadata with briefClockTime instead. */
export function shortBriefTimes(text: string, language: string, t: Translate): string {
    const machineValues = /https?:\/\/[^\s]+|\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})/g;
    const spans = [...text.matchAll(machineValues)];
    if (spans.length > 0) {
        let last = 0;
        let out = "";
        for (const span of spans) {
            const start = span.index!;
            out += shortBriefTimes(text.slice(last, start), language, t) + span[0];
            last = start + span[0].length;
        }
        return out + shortBriefTimes(text.slice(last), language, t);
    }
    const localized = text.replace(/(凌晨|早上|上午|中午|下午|晚上)\s*([0-2]?\d):([0-5]\d)(?::[0-5]\d)?/g, (value, period: string, h: string, m: string) => {
        let hour = Number(h);
        if (hour >= 24) return value;
        if (["凌晨", "早上", "上午", "晚上"].includes(period) && hour === 12) hour = 0;
        else if (["中午", "下午", "晚上"].includes(period) && hour < 12) hour += 12;
        return shortClock(hour, Number(m), language, t);
    });
    return localized.replace(/\b([0-2]?\d)(?::([0-5]\d)(?::[0-5]\d)?)?\s*([ap])\.?m\b/gi, (value, h: string, m: string | undefined, period: string) => {
        let hour = Number(h);
        if (hour < 1 || hour > 12) return value;
        hour = hour % 12 + (period.toLowerCase() === "p" ? 12 : 0);
        return shortClock(hour, Number(m ?? 0), language, t);
    });
}

/** A scheduler-generated label has known clock fields, unlike source prose. */
export function shortBriefClockLabel(text: string, language: string, t: Translate): string {
    // A clock already in the one format ("4:31 PM", "下午4:31") is left alone;
    // only a bare 24-hour clock ("09:00") is rewritten.
    return shortBriefTimes(text, language, t).replace(/(?<!(?:凌晨|早上|上午|中午|下午|晚上)\s*)\b([0-2]?\d):([0-5]\d)(?::[0-5]\d)?\b(?!\s*[AaPp]\.?[Mm]\b)/g, (value, h: string, m: string) => {
        const hour = Number(h);
        return hour < 24 ? shortClock(hour, Number(m), language, t) : value;
    });
}

export function briefClockTime(iso: string | undefined, language: string, t: Translate, timeZone?: string): string | null {
    if (!iso) return null;
    const when = new Date(iso);
    if (Number.isNaN(when.getTime())) return null;
    const options = { hour: "numeric", minute: "2-digit", hourCycle: "h23" } as const;
    let parts: Intl.DateTimeFormatPart[];
    try {
        parts = new Intl.DateTimeFormat("en-GB", { ...options, timeZone }).formatToParts(when);
    } catch {
        parts = new Intl.DateTimeFormat("en-GB", options).formatToParts(when);
    }
    const hour = Number(parts.find((part) => part.type === "hour")?.value);
    const minute = Number(parts.find((part) => part.type === "minute")?.value);
    return shortClock(hour, minute, language, t);
}

export function briefDateTime(value: string | number | undefined, language: string, t: Translate, timeZone?: string): string | null {
    if (value === undefined || value === "") return null;
    const when = new Date(value);
    if (Number.isNaN(when.getTime())) return null;
    const options = { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" } as const;
    let parts: Intl.DateTimeFormatPart[];
    try {
        parts = new Intl.DateTimeFormat(language, { ...options, timeZone }).formatToParts(when);
    } catch {
        parts = new Intl.DateTimeFormat(language, options).formatToParts(when);
    }
    const isClockPart = (part: Intl.DateTimeFormatPart) => ["hour", "minute", "dayPeriod"].includes(part.type);
    const first = parts.findIndex(isClockPart);
    const last = parts.reduce((last, part, index) => isClockPart(part) ? index : last, first);
    return parts.slice(0, first).map((part) => part.value).join("")
        + briefClockTime(when.toISOString(), language, t, timeZone)
        + parts.slice(last + 1).map((part) => part.value).join("");
}

/** A named day and the shared clock, joined by translated prose. */
export function briefDateAtTime(value: string | number | undefined, language: string, t: Translate): string | null {
    if (value === undefined || value === "") return null;
    const when = new Date(value);
    if (Number.isNaN(when.getTime())) return null;
    return t("todayBrief.clock.dateAtTime", {
        date: when.toLocaleDateString(language, { month: "short", day: "numeric" }),
        time: clockTime(when, language, t),
    });
}

/** A local time of day in the app's one clock, for components outside
 *  Today's translations. */
export function computerClock(when: Date, language = "en"): string {
    return clockTime(when, language, (key, options) => i18n.t(key, { ...options, ns: "today", lng: language }) as string);
}

/** "Yesterday, 7:01 PM" or "Thu, 10:58 PM" (chat-review v1.0.2 #3); today's
 *  time alone. */
export function computerDayClock(when: Date, language = "en", now = new Date()): string {
    const time = computerClock(when, language);
    if (when.toDateString() === now.toDateString()) return time;
    const yesterday = new Date(now);
    yesterday.setDate(now.getDate() - 1);
    if (when.toDateString() === yesterday.toDateString()) return i18n.t("todayBrief.clock.yesterdayAt", { ns: "today", lng: language, time });
    const day = new Intl.DateTimeFormat(language, { weekday: "short", ...(now.getTime() - when.getTime() > 6 * 86_400_000 ? { month: "short", day: "numeric" } : {}) }).format(when);
    return i18n.t("todayBrief.clock.dayAt", { ns: "today", lng: language, day, time });
}

/** Intl options that keep a date-and-time label on the computer's clock. */
export function computerHourOptions(): Intl.DateTimeFormatOptions {
    return computerUses24Hour ? { hourCycle: "h23" } : { hourCycle: "h12" };
}
