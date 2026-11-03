import type { TFunction } from "i18next";
import { clockTime } from "@/lib/brief-time";

type AppT = TFunction<"app">;
/** Today's clock translations (todayBrief.clock.*). */
type ClockT = (key: string, options?: Record<string, unknown>) => string;

const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();

/** A slot's local "HH:MM" in the app's one clock: "7:00 AM", "上午7:00". */
export function briefSlotClock(time: string, language: string, tr: ClockT): string {
    const match = /^(\d{1,2}):(\d{2})$/.exec(time);
    if (!match) return "";
    const at = new Date();
    at.setHours(Number(match[1]), Number(match[2]), 0, 0);
    return clockTime(at, language, tr);
}

/** "Every day, 7:00 AM": the same on a brief's row and in its detail. */
export function briefEvery(time: string, language: string, t: AppT, tr: ClockT): string {
    const clock = briefSlotClock(time, language, tr);
    return clock ? t("briefDetail.every", { time: clock }) : "";
}

/** The scheduler's next run in words: "Next tomorrow", or "Next today, 6:00 PM"
 *  for a run still due today, a late one included. */
export function briefNext(nextRunAt: number, now: Date, language: string, t: AppT, tr: ClockT): string {
    if (!(nextRunAt > 0)) return "";
    const at = new Date(nextRunAt);
    const days = Math.round((startOfDay(at) - startOfDay(now)) / 86_400_000);
    if (days <= 0) return t("briefDetail.nextTodayAt", { time: clockTime(at.getTime() < now.getTime() ? now : at, language, tr) });
    if (days === 1) return t("briefDetail.nextTomorrow");
    return t("briefDetail.nextDay", { day: at.toLocaleDateString(language, { weekday: "long" }) });
}
