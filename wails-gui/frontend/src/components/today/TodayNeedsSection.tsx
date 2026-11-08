import { measureToday } from "@/lib/today-telemetry";
import { useTranslation } from "react-i18next";
import type { TodayNeed, TodayNeedResolution } from "@/lib/wails-shim-app";
import { useTodaySectionSeen } from "@/hooks/useTodaySectionSeen";
import { Button } from "@/components/ui/button";
import { todayReviewSentence } from "@/lib/today-review-sentence";
import { computerClock } from "@/lib/brief-time";

/** The preview uses the first three rows of the shared Tasks projection. */
export function TodayNeedsSection({ items, onOpen, onOpenTasks, receipts = [], failures = {}, active = true }: {
    items: readonly TodayNeed[];
    receipts?: readonly { item: TodayNeed; result: TodayNeedResolution; index: number }[];
    active?: boolean;
    failures?: Readonly<Record<string, number>>;
    onOpen: (item: TodayNeed, index: number) => void;
    onOpenTasks: () => void;
}) {
    const { t, i18n } = useTranslation("today");
    const receiptById = new Map(receipts.map((receipt) => [receipt.item.id, receipt]));
    const visible = items.filter((item) => !receiptById.has(item.id));
    for (const receipt of [...receipts].sort((a, b) => a.index - b.index)) visible.splice(Math.min(receipt.index, visible.length), 0, receipt.item);
    const rows = visible.slice(0, 3);
    const seenRef = useTodaySectionSeen(active, "needs_you", rows.length);
    if (visible.length === 0) return null;
    const currentIds = new Set(items.map((item) => item.id));
    const isReceipt = (item: TodayNeed) => receiptById.has(item.id);
    const more = items.length - rows.filter((row) => currentIds.has(row.id) && !isReceipt(row)).length;
    return (
        <section ref={seenRef} aria-label={t("needsYou.heading")} className="space-y-3" data-testid="today-needs-you">
            <h2 className="text-[16px] font-semibold text-ink">{t("needsYou.heading")}</h2>
            <div className="divide-y divide-border rounded-xl border border-border bg-panel px-[18px]">
                {rows.map((item, index) => {
                    const receipt = receiptById.get(item.id);
                    if (receipt?.result.kind === "you_replied" && receipt.result.at) {
                        const time = computerClock(new Date(receipt.result.at), i18n.resolvedLanguage);
                        const text = receipt.result.person ? t("needsYou.alreadyReplied", { person: receipt.result.person, time }) : t("needsYou.alreadyRepliedNoName", { time });
                        return <p key={item.id} role="status" className="py-3 text-[14px] text-muted">{text}</p>;
                    }
                    if (failures[item.id]) return <p key={item.id} role="alert" className="py-3 text-[14px] text-muted">
                        {failures[item.id] > 1 ? t("needsYou.stillFailed") : t("needsYou.couldNotCheck")} {" "}
                        <button type="button" className="text-accent" onClick={() => measureToday("D01", "open", "opened", () => onOpen(item, index))}>{t("needsYou.tryAgain")}</button>
                    </p>;
                    if (receipt?.result.kind === "resolved") return <p key={item.id} role="status" className="py-3 text-[14px] text-muted">{t("needsYou.noLongerNeeded")}</p>;
                    const time = item.createdAt > 0 ? computerClock(new Date(item.createdAt), i18n.resolvedLanguage) : "";
                    return <div key={item.id} className="flex items-center gap-3 py-3">
                        <span aria-hidden="true" className="size-2 shrink-0 rounded-full bg-copper" />
                        <div className="min-w-0 flex-1">
                            <p className="text-[14px] text-ink">{todayReviewSentence(item, i18n.resolvedLanguage)}</p>
                            <p className="mt-1 text-[12px] text-muted">{t("needsYou.source", { source: item.source || t("needsYou.isoform") })}{time ? ` · ${time}` : ""}</p>
                        </div>
                        <Button size="sm" variant="secondary" onClick={() => measureToday("D01", "open", "opened", () => onOpen(item, index))}>{t(item.kind === "commitment_slipping" ? "holds.findTime" : item.kind === "work_blocked" ? "needsYou.answer" : "needsYou.review")}</Button>
                    </div>;
                })}
            </div>
            {more > 0 && <Button variant="quiet" size="sm" onClick={() => measureToday("T17", "open", "opened", onOpenTasks)}>{t("needsYou.more", { count: more })}</Button>}
        </section>
    );
}
