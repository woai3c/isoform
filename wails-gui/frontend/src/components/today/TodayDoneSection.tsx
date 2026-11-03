import { measureToday } from "@/lib/today-telemetry";
import { Check } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { useTodaySectionSeen } from "@/hooks/useTodaySectionSeen";
import type { TodayDone } from "@/lib/wails-shim-app";
import { todayReviewSentence } from "@/lib/today-review-sentence";
import { computerClock } from "@/lib/brief-time";

export function TodayDoneSection({ items, onOpen, onOpenTasks, active = true }: {
    items: readonly TodayDone[];
    onOpen: (item: TodayDone) => void;
    onOpenTasks: () => void;
    active?: boolean;
}) {
    const { t, i18n } = useTranslation("today");
    const seenRef = useTodaySectionSeen(active, "done_for_you", Math.min(items.length, 3));
    if (items.length === 0) return null;
    const rows = items.slice(0, 3);
    const more = items.length - rows.length;
    return <section ref={seenRef} aria-label={t("doneForYou.heading")} className="space-y-3" data-testid="today-done-for-you">
        <h2 className="text-[16px] font-semibold text-ink">{t("doneForYou.heading")}</h2>
        <div className="divide-y divide-border rounded-xl border border-border bg-panel px-[18px]">
            {rows.map((item) => {
                const time = item.at > 0 ? computerClock(new Date(item.at), i18n.resolvedLanguage) : "";
                return <div key={item.id} className="flex items-center gap-3 py-3">
                    <Check aria-hidden="true" className="size-4 shrink-0 text-accent" />
                    <button className="min-w-0 flex-1 text-left" onClick={() => measureToday("T21", "open", "opened", () => onOpen(item))}>
                        <span className="block text-[14px] text-ink">{todayReviewSentence(item, i18n.resolvedLanguage)}</span>
                        {time && <span className="mt-1 block text-[12px] text-muted">{time}</span>}
                    </button>
                    {item.kind !== "answer_ready" && <Button size="sm" variant="secondary" onClick={() => measureToday("T21", "open", "opened", () => onOpen(item))}>{t("doneForYou.review")}</Button>}
                </div>;
            })}
        </div>
        {more > 0 && <Button variant="quiet" size="sm" onClick={() => measureToday("T17", "open", "opened", onOpenTasks)}>{t("doneForYou.more", { count: more })}</Button>}
    </section>;
}
