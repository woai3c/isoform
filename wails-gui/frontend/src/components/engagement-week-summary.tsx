import { useCallback, useEffect, useRef, useState } from "react";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { GetEngagementWeekSummary, type EngagementWeekSummary } from "@/lib/wails-shim-app";
import { Button } from "./ui/button";

const STATES = ["created", "scheduled", "sent", "delivered", "opened", "acted", "dismissed", "muted"] as const;

export interface EngagementWeekState {
    summary: EngagementWeekSummary | null;
    failed: boolean;
    refresh: () => Promise<void>;
}

/** The last 7 days of notices, read again when the window regains focus.
 *  Weekly counts share the hub's receipt aggregation with the phone. */
export function useEngagementWeek(): EngagementWeekState {
    const [summary, setSummary] = useState<EngagementWeekSummary | null>(null);
    const [failed, setFailed] = useState(false);
    const readVersion = useRef(0);

    const refresh = useCallback(async () => {
        const version = ++readVersion.current;
        try {
            const next = await GetEngagementWeekSummary();
            if (version !== readVersion.current) return;
            setSummary(next);
            setFailed(false);
        } catch {
            if (version === readVersion.current) setFailed(true);
        }
    }, []);

    useEffect(() => {
        void refresh();
        const onFocus = () => void refresh();
        window.addEventListener("focus", onFocus);
        return () => {
            readVersion.current += 1;
            window.removeEventListener("focus", onFocus);
        };
    }, [refresh]);

    return { summary, failed, refresh };
}

/** "Isoform spoke up 9 times this week, you acted on 6." Notifications keeps
 *  it beside the 30 days; Claude Design drops it from Today's header
 *  (2026-10-22), which leaves this page as its home. */
export function EngagementWeekLine({ week }: { week: EngagementWeekState }) {
    const { t } = useTranslation("settings");
    const { summary, failed } = week;
    if (failed) {
        return (
            <p className="flex flex-wrap items-center gap-x-2 text-[13px] text-muted">
                <span role="alert" className="text-error">{t("proactiveNotifications.engagement.loadFailed")}</span>
                <Button variant="quiet" size="sm" onClick={() => void week.refresh()}>{t("proactiveNotifications.retry")}</Button>
            </p>
        );
    }
    if (!summary?.available) return null;
    return (
        <p className="text-[13px] text-ink">
            {summary.sent === 0
                ? t("proactiveNotifications.ranked.weekQuiet")
                : t("proactiveNotifications.ranked.weekSpokeUp", { count: summary.sent, actedOn: summary.actedOn })}
        </p>
    );
}

/** The last 7 days, counted per kind and per state. Notifications shows it
 *  folded under the 30-day list, from the page's one week read. */
export function EngagementWeekSummary({ week }: { week: EngagementWeekState }) {
    const { t, i18n } = useTranslation("settings");
    const { summary, failed, refresh } = week;

    const formatRate = (value: number) => new Intl.NumberFormat(i18n.resolvedLanguage, {
        style: "percent", maximumFractionDigits: 0,
    }).format(value);

    return (
        <div className="flex flex-col">
            <p className="settings-card-desc mb-2">{t("proactiveNotifications.engagement.description")}</p>
            {failed ? (
                <div className="flex items-center justify-between gap-3">
                    <p role="alert" className="text-xs text-error">{t("proactiveNotifications.engagement.loadFailed")}</p>
                    <Button variant="outline" size="sm" onClick={() => void refresh()}>{t("proactiveNotifications.retry")}</Button>
                </div>
            ) : !summary ? (
                <p className="flex items-center gap-2 text-xs text-muted">
                    <Loader2 className="size-3.5 animate-spin" />
                    {t("proactiveNotifications.engagement.loading")}
                </p>
            ) : !summary.available ? (
                <p className="text-xs text-muted">{t("proactiveNotifications.unavailable")}</p>
            ) : (
                <>
                    <p className="mb-3 text-xs text-muted">{t("proactiveNotifications.engagement.measurement")}</p>
                    <div className="overflow-x-auto">
                        <table className="w-full text-[11px] tabular-nums" aria-label={t("proactiveNotifications.engagement.title")}>
                            <thead>
                                <tr className="border-b border-border text-muted">
                                    <th scope="col" className="py-2 pr-3 text-left font-medium">{t("proactiveNotifications.engagement.kind")}</th>
                                    {STATES.map((state) => <th key={state} scope="col" className="whitespace-nowrap px-2 py-2 text-right font-medium">{t(`proactiveNotifications.engagement.states.${state}`)}</th>)}
                                    <th scope="col" className="whitespace-nowrap py-2 pl-2 text-right font-medium">{t("proactiveNotifications.engagement.actRate")}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {(summary.kinds ?? []).map((kind) => (
                                    <tr key={kind.kind} className="border-b border-border last:border-0">
                                        <th scope="row" className="min-w-28 py-2 pr-3 text-left font-normal text-ink">{t(`proactiveNotifications.kinds.${kind.kind}.label` as never) as string}</th>
                                        {STATES.map((state) => <td key={state} className="px-2 py-2 text-right text-muted">{kind[state]}</td>)}
                                        <td className="py-2 pl-2 text-right font-medium text-ink">{kind.sent > 0 ? formatRate(kind.actRate) : t("proactiveNotifications.engagement.noRate")}</td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                    {summary.sent === 0 && <p className="mt-3 text-xs text-muted">{t("proactiveNotifications.engagement.empty")}</p>}
                </>
            )}
        </div>
    );
}
