import { briefClockTime, briefSlotKey } from "@/lib/brief-time";
import type { BriefScheduleRun, MobileBriefDocument } from "@/lib/wails-shim-app";

type Translate = (key: string, options?: Record<string, unknown>) => string;

/** The kind a brief's title names (design section 1). A brief asked for is
 *  named by the time it was made, before noon morning and from noon evening
 *  (today-review v1.1.16 D14); without a time it takes the slot the computer
 *  recorded (displaySlot), else it is "Brief". */
export function briefKind(brief: Pick<MobileBriefDocument, "slot" | "yourWeek"> & { asOf?: string; timezone?: string; displaySlot?: MobileBriefDocument["displaySlot"] }): "week" | "morning" | "evening" | "on_request" {
    if (brief.yourWeek) return "week";
    if (brief.slot === "morning" || brief.slot === "evening") return brief.slot;
    if (brief.asOf && !Number.isNaN(new Date(brief.asOf).getTime())) return briefSlotKey(undefined, brief.asOf, brief.timezone || undefined);
    const slot = brief.displaySlot ?? brief.slot;
    return slot === "morning" || slot === "evening" ? slot : "on_request";
}

/** An early brief was made before its slot (P157): generation.reason is
 *  "made_ahead". */
export function briefMadeAhead(brief: { generation?: { reason?: string } | null }): boolean {
    return brief.generation?.reason === "made_ahead";
}

/** Where a made-ahead brief's made time falls against its day (early-brief v1.0.1):
 *  "day" (the brief's own day), "lastNight" (the day before, 6 PM or later),
 *  "yesterday" (the day before, before 6 PM), or "other" (earlier than that). */
export function briefMadeAheadWhen(brief: { asOf?: string; localDate?: string; timezone?: string }): "day" | "lastNight" | "yesterday" | "other" {
    if (!brief.asOf || !brief.localDate) return "other";
    const made = new Date(brief.asOf);
    if (Number.isNaN(made.getTime())) return "other";
    const timeZone = brief.timezone || undefined;
    try {
        const day = made.toLocaleDateString("en-CA", { timeZone });
        const hour = Number(made.toLocaleTimeString("en-GB", { hour: "2-digit", hourCycle: "h23", timeZone }).split(":")[0]);
        if (day === brief.localDate) return "day";
        if (day === previousDay(brief.localDate)) return hour >= 18 ? "lastNight" : "yesterday";
        return "other";
    } catch {
        return "other";
    }
}

/** The calendar day before an ISO date (YYYY-MM-DD). */
export function previousDay(isoDate: string): string {
    const [year, month, day] = isoDate.split("-").map(Number);
    return new Date(Date.UTC(year, month - 1, day - 1)).toISOString().slice(0, 10);
}

/** The made-at phrase of a made-ahead brief, without its kind: "made at 1:20 AM",
 *  "made last night at 11:20 PM", "made yesterday at 3:10 PM" (zh-CN "1:20 生成",
 *  "昨晚 23:20 生成", "昨天 15:10 生成"). The clock is the one every zh-CN time uses. */
export function briefMadeAheadPhrase(brief: { asOf?: string; localDate?: string; timezone?: string }, language: string, tr: Translate): string {
    const time = briefClockTime(brief.asOf, language, tr, brief.timezone);
    // No time at all: no phrase, so the caller shows the slot name alone, never "made at " with nothing after it.
    if (!time) return "";
    const when = briefMadeAheadWhen(brief);
    if (when === "other") {
        // An older day: the date form, "made Nov 6 at 3:10 PM" (zh-CN "11月6日 15:10 生成").
        return tr("todayBrief.madeAhead.onDate", { date: briefDateText(brief, language), time });
    }
    const key = when === "lastNight" ? "todayBrief.madeAhead.lastNight" : when === "yesterday" ? "todayBrief.madeAhead.yesterday" : "todayBrief.madeAhead.at";
    return tr(key, { time });
}

/** The made day as a date: "Nov 6" (en), "11月6日" (zh-CN), in the brief's zone. */
function briefDateText(brief: { asOf?: string; timezone?: string }, language: string): string {
    const made = new Date(brief.asOf ?? "");
    const timeZone = brief.timezone || undefined;
    if (language.startsWith("zh")) {
        const parts = new Intl.DateTimeFormat("en-US", { month: "numeric", day: "numeric", timeZone }).formatToParts(made);
        return `${parts.find((p) => p.type === "month")?.value ?? ""}月${parts.find((p) => p.type === "day")?.value ?? ""}日`;
    }
    return new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", timeZone }).format(made);
}

/** English keeps "at 11:20 PM" together: its spaces become non-breaking, so a wrap falls before "at". */
export function keepTimeTogether(phrase: string): string {
    return phrase.replace(/ at (\d{1,2}:\d{2}) (AM|PM)$/, " at $1 $2");
}

/** The made-at line of a brief made before its slot: "Morning brief · made last
 *  night at 11:20 PM". The kind label is passed in, so the Today title and the Chat
 *  card name it alike. */
export function briefMadeAheadLine(brief: { asOf?: string; localDate?: string; timezone?: string }, kindLabel: string, language: string, tr: Translate): string {
    const phrase = briefMadeAheadPhrase(brief, language, tr);
    return phrase ? `${kindLabel} · ${phrase}` : kindLabel;
}

/** The Brief page's run row for an early brief: the slot's day, then its made-at
 *  phrase, as "Today · made last night at 11:20 PM". The row belongs to its slot's
 *  day (the run's ready time is when it was made, so the day is the slot's). */
export function briefMadeAheadRunLine(run: { briefId: string; at: string; timezone?: string }, dayNameOf: (isoDate: string) => string, language: string, tr: Translate, now: Date = new Date()): string {
    const slotDay = run.briefId.slice(0, 10);
    const today = now.toLocaleDateString("en-CA");
    const dayWord = slotDay === today ? tr("todayBrief.madeAhead.dayToday")
        : slotDay === previousDay(today) ? tr("todayBrief.madeAhead.dayYesterday")
            : dayNameOf(slotDay);
    const phrase = briefMadeAheadPhrase({ asOf: run.at, localDate: slotDay, timezone: run.timezone }, language, tr);
    if (!phrase) return dayWord;
    return `${dayWord} · ${language.startsWith("zh") ? phrase : keepTimeTogether(phrase)}`;
}

/** The card title: kind and the time the brief was made, as "Morning brief ·
 *  6:50 AM". The same title names the card, the player and the Chat card. */
export function briefTitle(brief: Pick<MobileBriefDocument, "slot" | "yourWeek" | "asOf" | "timezone"> & { briefId?: string; displaySlot?: MobileBriefDocument["displaySlot"]; localDate?: string; generation?: { reason?: string } | null }, language: string, tr: Translate, run?: BriefScheduleRun): string {
    const kind = briefKind(brief);
    const kindLabel = kind === "week" ? tr("todayBrief.kind.week") : tr(`todayBrief.slot.${kind}`);
    if (briefMadeAhead(brief)) {
        // The made-at time is the truth (R64): it is never shown as the slot's time.
        return briefMadeAheadLine(brief, kindLabel, language, tr);
    }
    const readyAt = run?.status === "ok" && run.briefId === brief.briefId && run.at ? run.at : brief.asOf;
    return tr("todayBrief.asOf", { slot: kindLabel, time: briefClockTime(readyAt, language, tr, brief.timezone) ?? "" });
}

/** The matching completed run supplies both times and any known cause. */
export function briefLateLine(brief: Pick<MobileBriefDocument, "briefId" | "timezone">, language: string, tr: Translate, run?: BriefScheduleRun): string | null {
    if (!run?.late || run.status !== "ok" || run.briefId !== brief.briefId) return null;
    const time = briefClockTime(run.scheduledAt, language, tr, brief.timezone);
    const at = briefClockTime(run.at, language, tr, brief.timezone);
    if (!time || !at) return null;
    const bare = time.replace(/^(?:上午|下午|凌晨|中午|晚上)/, "");
    const reason = run.reason === "asleep" || run.reason === "closed"
        ? tr(`app:briefDetail.lateReason.${run.reason}`) : "";
    return tr(reason ? "app:briefDetail.cameLate" : "app:briefDetail.cameLateNoReason", { at, time, bare, reason });

}

/** A brief has words to read; Play and any player need them with the audio. */
export function briefHasWords(brief: Pick<MobileBriefDocument, "narrative"> | null | undefined): boolean {
    return !!brief && Array.isArray(brief.narrative) && brief.narrative.some((paragraph) => paragraph.text?.trim());
}
