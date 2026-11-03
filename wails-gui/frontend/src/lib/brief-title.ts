import { briefClockTime } from "@/lib/brief-time";
import type { BriefScheduleRun, MobileBriefDocument } from "@/lib/wails-shim-app";

type Translate = (key: string, options?: Record<string, unknown>) => string;

/** The kind a brief's title names (design section 1). A brief asked for
 *  takes the slot the computer recorded it standing in for (displaySlot), so
 *  the player names the brief it plays (today-review v1.1.4 #7); without one
 *  it is "Brief". */
export function briefKind(brief: Pick<MobileBriefDocument, "slot" | "yourWeek"> & { displaySlot?: MobileBriefDocument["displaySlot"] }): "week" | "morning" | "evening" | "on_request" {
    if (brief.yourWeek) return "week";
    const slot = brief.slot === "morning" || brief.slot === "evening" ? brief.slot : brief.displaySlot ?? brief.slot;
    return slot === "morning" || slot === "evening" ? slot : "on_request";
}

/** The card title: kind and the time the brief was made, as "Morning brief ·
 *  6:50 AM". The same title names the card, the player and the Chat card. */
export function briefTitle(brief: Pick<MobileBriefDocument, "slot" | "yourWeek" | "asOf" | "timezone"> & { briefId?: string; displaySlot?: MobileBriefDocument["displaySlot"] }, language: string, tr: Translate, run?: BriefScheduleRun): string {
    const kind = briefKind(brief);
    const kindLabel = kind === "week" ? tr("todayBrief.kind.week") : tr(`todayBrief.slot.${kind}`);
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
