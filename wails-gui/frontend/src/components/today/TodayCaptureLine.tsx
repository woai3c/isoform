import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import type { CaptureCounts } from "@/lib/wails-shim-app";

/**
 * The words of the Today line: "Today Isoform saw 412 screens and heard
 * speech during 38 minutes", or Paused while observing is paused. null while
 * unknown, including while activity.db is still opening (zeros then would
 * wrongly say nothing was captured). Numbers are formatted for the language,
 * as on the Settings card.
 */
export function describeTodayCapture(
    counts: CaptureCounts | null,
    t: (key: string, options?: Record<string, unknown>) => string,
    language?: string,
): string | null {
    if (!counts) return null;
    if (counts.paused) return t("captured.paused");
    if (counts.notReady) return null;
    const format = new Intl.NumberFormat(language);
    const today = (source: string) => counts.sources?.find((row) => row.source === source)?.today ?? 0;
    const screens = today("screens");
    const minutes = today("audioMinutes");
    const screensText = t("captured.screens", { count: screens, n: format.format(screens) });
    // "for 2 hours", not "during 120 minutes" (activity review v1.0.7 AR52).
    const hours = Math.floor(minutes / 60);
    const hoursText = t("captured.hours", { count: hours, n: format.format(hours) });
    const restText = t("captured.minutes", { count: minutes % 60, n: format.format(minutes % 60) });
    const minutesText = hours === 0
        ? t("captured.minutes", { count: minutes, n: format.format(minutes) })
        : minutes % 60 === 0 ? hoursText : t("captured.hoursMinutes", { hours: hoursText, minutes: restText });
    if (screens > 0 && minutes > 0) {
        return t("captured.screensAndAudio", { screens: screensText, minutes: minutesText });
    }
    if (screens > 0) return t("captured.screensOnly", { screens: screensText });
    if (minutes > 0) return t("captured.audioOnly", { minutes: minutesText });
    return t("captured.nothing");
}

/**
 * The capture line among the Today status lines. It opens the "What Isoform
 * captured" card in Settings > Observation.
 */
export function TodayCaptureLine({ text, onOpen }: { text: string; onOpen?: () => void }) {
    const { t } = useTranslation("today");
    if (!onOpen) {
        return (
            <p data-testid="today-capture" className="text-[12px] leading-snug text-muted">
                {text}
            </p>
        );
    }
    return (
        <Button
            variant="link"
            type="button"
            data-testid="today-capture"
            title={t("captured.open")}
            onClick={onOpen}
            className="h-auto rounded-sm p-0 text-[12px] font-normal leading-snug text-accent outline-none transition-colors hover:text-accent focus-visible:ring-1 focus-visible:ring-border-focus"
        >
            {text}
        </Button>
    );
}
