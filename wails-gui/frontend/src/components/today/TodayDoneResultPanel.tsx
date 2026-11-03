import { measureToday } from "@/lib/today-telemetry";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import type { TodayDone } from "@/lib/wails-shim-app";
import { todayReviewSentence } from "@/lib/today-review-sentence";

/** The exact finished status, with its destination named separately. */
export function TodayDoneResultPanel({ item, onClose, onOpenTarget }: { item: TodayDone; onClose: () => void; onOpenTarget: (item: TodayDone) => void }) {
    const { t, i18n } = useTranslation("today");
    const { t: common } = useTranslation("common");
    const label = item.targetKind === "playbook" ? t("doneForYou.openRoutine") : item.targetKind === "automation" ? t("doneForYou.openAutomation") : null;
    return <aside data-testid="today-done-result-panel" className="flex h-full min-h-0 flex-col bg-background-accent">
        <header className="flex items-center justify-between border-b border-border px-4 py-3">
            <h2 className="text-sm font-semibold text-ink">{t("doneForYou.heading")}</h2>
            <Button size="icon" variant="quiet" aria-label={common("close")} onClick={() => measureToday("T21", "open", "opened", onClose)}><X className="size-4" /></Button>
        </header>
        <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-4">
            <p className="text-sm text-ink">{todayReviewSentence(item, i18n.resolvedLanguage)}</p>
            {label && <Button size="sm" variant="secondary" onClick={() => measureToday("T21", "open", "opened", () => onOpenTarget(item))}>{label}</Button>}
        </div>
    </aside>;
}
