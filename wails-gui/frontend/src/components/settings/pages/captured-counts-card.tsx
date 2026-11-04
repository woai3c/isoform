import { SettingsFailureLine } from "@/components/settings/settings-failure";
import { useTranslation } from "react-i18next";
import type { CaptureCountSource } from "@/lib/wails-shim-app";
import { useCaptureCounts } from "@/hooks/useCaptureCounts";
import { SettingsCard } from "@/components/settings/settings-primitives";
import { settingsAnchor } from "@/components/settings/settings-advanced";
import { SettingsReason } from "./capture-shared";

/** Deep-link anchor of the card: openSettings("activity", { anchor }). */
export const CAPTURED_COUNTS_ANCHOR = "captured";

// The sources the card has words for (observation.captured.sources.*). Rows
// keep the order Go lists them in; a source Go adds later stays hidden until
// it gets a label.
const KNOWN_SOURCES = new Set<CaptureCountSource>([
    "screens",
    "textRead",
    "audioMinutes",
    "meetings",
    "typing",
    "calendarEvents",
    "calendarPeeks",
]);

/**
 * "What Isoform captured": one row per source with today's count and the
 * last seven days', read from what Isoform keeps on this computer. While
 * observing is paused it says Paused instead of showing numbers, and while
 * activity.db is still opening it shows no numbers at all.
 */
export function CapturedCountsCard() {
    const { t, i18n } = useTranslation("settings");
    const { counts, loadFailed, refresh } = useCaptureCounts();
    const format = new Intl.NumberFormat(i18n.language);
    // A source with nothing this week has nothing to say yet.
    const rows = (counts?.sources ?? []).filter((row) => KNOWN_SOURCES.has(row.source) && row.week > 0);

    if (counts && !counts.paused && !counts.notReady && rows.length === 0 && !loadFailed) return null;

    return (
        <div {...settingsAnchor(CAPTURED_COUNTS_ANCHOR)}>
            <SettingsCard title={t("observation.captured.title")} description={t("observation.captured.description")}>
                {loadFailed && <SettingsFailureLine failure={{ text: t("observation.captured.loadFailed"), retry: () => void refresh() }} />}
                {counts?.paused && <SettingsReason>{t("observation.captured.paused")}</SettingsReason>}
                {counts && !counts.paused && !counts.notReady && rows.length > 0 && (
                    <table className="w-full text-[13px] leading-[1.4]" data-testid="captured-counts">
                        <thead>
                            <tr className="text-xs text-[color:var(--s-muted)]">
                                <th scope="col" className="pb-1.5 text-left font-normal">
                                    <span className="sr-only">{t("observation.captured.source")}</span>
                                </th>
                                <th scope="col" className="w-24 pb-1.5 text-right font-normal">
                                    {t("observation.captured.today")}
                                </th>
                                <th scope="col" className="w-24 pb-1.5 text-right font-normal">
                                    {t("observation.captured.week")}
                                </th>
                            </tr>
                        </thead>
                        <tbody>
                            {rows.map((row) => (
                                <tr key={row.source} className="border-t border-[color:var(--s-divider)]">
                                    <th scope="row" className="py-2.5 text-left font-medium text-[color:var(--s-ink-strong)]">
                                        {t(`observation.captured.sources.${row.source}` as never)}
                                    </th>
                                    <td className="py-2.5 text-right tabular-nums text-[color:var(--s-ink-strong)]">{format.format(row.today)}</td>
                                    <td className="py-2.5 text-right tabular-nums text-[color:var(--s-muted)]">{format.format(row.week)}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                )}
            </SettingsCard>
        </div>
    );
}
