import { measureToday } from "@/lib/today-telemetry";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { GetTodayBriefByID, type MobileBriefDocument } from "@/lib/wails-shim-app";
import { onPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";
import { briefKind } from "@/lib/brief-title";
import { clockTime } from "@/lib/brief-time";

type Translate = (key: string, options?: Record<string, unknown>) => string;

/** An earlier brief, read in the panel beside Today while this slot's brief
 *  is missing (today-review v1.1.5 #1). Its title carries its day, and it
 *  has no question to answer: answering it today would act on stale facts. */
export function TodayLastBriefPanel({ briefId, onClose }: { briefId: string; onClose: () => void }) {
    const { t, i18n } = useTranslation("today");
    const { t: common } = useTranslation("common");
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const [brief, setBrief] = useState<MobileBriefDocument | null>(null);
    const [failed, setFailed] = useState(false);
    const closeRef = useRef(onClose);
    closeRef.current = onClose;
    useEffect(() => {
        let live = true;
        setBrief(null); setFailed(false);
        Promise.resolve().then(() => GetTodayBriefByID(briefId)).then((view) => {
            if (!live) return;
            // Only the brief the link names, never a fallback brief.
            if (view?.brief?.briefId === briefId) setBrief(view.brief);
            else setFailed(true);
        }).catch(() => { if (live) setFailed(true); });
        const withdraw = onPagePrivacyWithdrawal(() => { live = false; setBrief(null); closeRef.current(); });
        return () => { live = false; withdraw(); };
    }, [briefId]);
    const language = i18n.language;
    const at = brief ? new Date(brief.asOf) : null;
    const kind = brief ? briefKind(brief) : null;
    const title = brief && at && !Number.isNaN(at.getTime())
        ? tr("todayBrief.pending.lastBriefTitle", { brief: tr(`todayBrief.slot.${kind === "morning" || kind === "evening" ? kind : "on_request"}`), day: at.toLocaleDateString(language, { weekday: "short" }), time: clockTime(at, language, tr) })
        : "";
    const paragraphs = brief?.narrative.map((paragraph) => paragraph.text?.trim() ?? "").filter(Boolean) ?? [];
    return (
        <aside data-testid="today-last-brief-panel" className="flex h-full min-h-0 flex-col bg-background-accent">
            <header className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
                <h2 className="min-w-0 truncate text-sm font-semibold text-ink">{title}</h2>
                <Button size="icon" variant="quiet" aria-label={common("close")} onClick={() => measureToday("T05", "open", "opened", onClose)}><X className="size-4" /></Button>
            </header>
            <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4 py-4">
                {paragraphs.map((text, index) => <p key={index} className="whitespace-pre-wrap break-words text-[14px] leading-normal text-ink">{text}</p>)}
                {failed && <p role="alert" className="text-[13px] text-muted">{tr("todayBrief.loadFailed")}</p>}
            </div>
        </aside>
    );
}
