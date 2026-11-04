import { useTranslation } from "react-i18next";
import type { MobileBriefHealth } from "@/lib/wails-shim-app";
import { clockTime } from "@/lib/brief-time";
import { Button } from "@/components/ui/button";
import { RecordTodayAction } from "@/lib/wails-shim-app";
import { BriefSignIn } from "./BriefSignIn";
import { TodayCaptureLine } from "./TodayCaptureLine";

/** One quiet line under the status lines: a notice kind the user moved from
 *  notifications to Today (RC 1). The text arrives ready to show. */
export interface TodayQuietLine {
    id: string;
    text: string;
}

/**
 * Says when a brief ran or runs, relative to today: "today at 7:02 AM",
 * "yesterday at 6:00 PM", "tomorrow at 7:00 AM", otherwise the date and
 * time, in the one clock Today uses.
 */
export function describeBriefTime(
    iso: string,
    now: Date,
    t: (key: string, options?: Record<string, unknown>) => string,
    language = "en",
): string | null {
    if (!iso) return null;
    const when = new Date(iso);
    if (Number.isNaN(when.getTime())) return null;
    const time = clockTime(when, language, t);
    const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
    const dayDiff = Math.round((startOfDay(when) - startOfDay(now)) / 86_400_000);
    if (dayDiff === 0) return t("status.todayAt", { time });
    if (dayDiff === -1) return t("status.yesterdayAt", { time });
    if (dayDiff === 1) return t("status.tomorrowAt", { time });
    return `${when.toLocaleDateString(language, { weekday: "short", month: "short", day: "numeric" })} ${time}`;
}

/**
 * The quiet status lines under the Today header: how the scheduled brief is
 * doing (a missed brief's one Make it now is in its card, today-review
 * v1.1.5 #2), what Isoform captured today, then one muted line per notice kind
 * shown on Today instead. How often Isoform spoke up this week stays in
 * Settings > Notifications (Claude Design, 2026-10-22). Renders nothing while
 * all of them are unknown, so the header never shows an empty box. The
 * spoken brief plays from the brief's own card (TodayBriefSection).
 */
export function TodayStatus({
    brief,
    now = new Date(),
    capture,
    quiet = [],
    date,
    onMissedWhy,
    building = false,
}: {
    brief: MobileBriefHealth | null;
    building?: boolean;
    date?: string;
    /** Opens the missed brief's own detail (Tasks › Scheduled). */
    onMissedWhy?: (slot: "morning" | "evening") => void;
    now?: Date;
    /** What Isoform captured today (describeTodayCapture), with the link to
     *  its card in Settings. */
    capture?: { text: string; onOpen?: () => void } | null;
    /** Quieted notice kinds; each is a line only when it has words. */
    quiet?: TodayQuietLine[];
}) {
    const { t, i18n } = useTranslation("today");
    const tr = (key: string, options?: Record<string, unknown>) =>
        t(key as never, options as never) as unknown as string;
    const openWhy = (slot: "morning" | "evening") => {
        if (!onMissedWhy) return;
        const started = performance.now();
        onMissedWhy(slot);
        void RecordTodayAction("today", "T86", "open", "opened", Math.max(0, Math.round(performance.now() - started))).catch(() => {});
    };
    const pendingDate = brief?.pendingAt ? new Date(brief.pendingAt) : null;
    const pending = brief?.enabled && (brief.pendingSlot === "morning" || brief.pendingSlot === "evening") && pendingDate && !Number.isNaN(pendingDate.getTime()) ? { slot: brief.pendingSlot, time: clockTime(pendingDate, i18n.language, tr) } : null;

    const signedOut = !!pending && brief?.pendingCause === "signed_out" && !!brief.pendingAssistant && (brief.pendingAttempts ?? 0) > 0;
    const retryDate = pending && brief?.retryAt ? new Date(brief.retryAt) : null;
    const tries = retryDate && !Number.isNaN(retryDate.getTime()) && retryDate.getTime() > now.getTime() && (brief?.pendingAttempts ?? 0) > 0
        ? t(brief?.pendingCause === "timeout" ? "todayBrief.pending.triedTimeout" : brief?.pendingCause === "signed_out" ? "todayBrief.pending.triedSignedOut" : "todayBrief.pending.tried", { count: brief?.pendingAttempts ?? 0, time: clockTime(retryDate, i18n.language, tr) })
        : "";
    const nextDate = brief?.enabled && brief.nextRunAt ? new Date(brief.nextRunAt) : null;
    const next = nextDate && !Number.isNaN(nextDate.getTime()) ? clockTime(nextDate, i18n.language, tr) : null;
    // A brief made in the last two hours skips the next slot, so the next
    // one can be tomorrow's (today-review v1.1.16 D17).
    const nextTomorrow = !!next && !!nextDate && new Date(nextDate.getFullYear(), nextDate.getMonth(), nextDate.getDate()).getTime() - new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime() === 86_400_000;

    // A slot with no try left has given way: say what comes next (today-review v1.1.22 R51).
    const givenWay = !!pending && !building && !brief?.retryAt && !signedOut && (pending.slot === "morning" ? now.getHours() >= 12 : now.getHours() < 12);
    const nextSlot = brief?.nextSlot === "morning" || brief?.nextSlot === "evening" ? brief.nextSlot : nextDate && nextDate.getHours() < 12 ? "morning" : "evening";

    const quietLines = quiet.filter((line) => line.text.trim());
    if (!date && !next && !pending && !capture && quietLines.length === 0) return null;

    return (
        <div
            data-testid="today-status"
            className="flex flex-wrap items-baseline gap-x-3.5 gap-y-1 text-[12.5px] leading-snug text-muted"
        >
            {building && pending ? <span>{t(pending.slot === "morning" ? "todayBrief.pending.makingMorning" : "todayBrief.pending.makingEvening")}</span> : signedOut ? <span className="text-copper">{t("todayBrief.pending.signedOut", { count: brief?.pendingAttempts ?? 0 })}{" "}<BriefSignIn assistant={brief!.pendingAssistant!} size="12.5px" /></span> : tries ? <span>{tries}</span> : givenWay && pending ? <span>{t(pending.slot === "morning" ? "status.missedMorning" : "status.missedEvening", { time: pending.time })}{givenWay && next && <span data-testid="today-status-next">{i18n.language.startsWith("zh") ? "" : " "}{t(nextSlot === "morning" ? nextTomorrow ? "status.nextMorningTomorrowAt" : "status.nextMorningAt" : nextTomorrow ? "status.nextEveningTomorrowAt" : "status.nextEveningAt", { time: next })}</span>}{onMissedWhy && <> · <Button variant="link" size="sm" className="h-auto p-0 text-[12.5px] font-medium hover:no-underline" onClick={() => openWhy(pending.slot as "morning" | "evening")}>{t("status.why")}</Button></>}</span> : null}
            {(!givenWay || building || tries) && (date || next) && <span data-testid="today-status-date">{date}{date && next && " · "}{next && t(nextTomorrow ? "status.nextBriefTomorrowAt" : "status.nextBriefAt", { time: next })}</span>}
            {capture && <TodayCaptureLine text={capture.text} onOpen={capture.onOpen} />}
            {quietLines.map((line) => (
                <p key={line.id} data-testid="today-quiet-line">{line.text}</p>
            ))}
        </div>
    );
}
