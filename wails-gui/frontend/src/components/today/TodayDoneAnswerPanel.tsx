import { measureToday } from "@/lib/today-telemetry";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { MarkdownContent } from "@/components/markdown-content";
import { MarkTodayDoneOpened, ReadTodayDoneAnswer, type TodayDone } from "@/lib/wails-shim-app";
import { onPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";

/** Reopen the exact answer through Go's current source and privacy checks. */
export function TodayDoneAnswerPanel({ item, onClose }: { item: TodayDone; onClose: () => void }) {
    const { t } = useTranslation("today");
    const { t: common } = useTranslation("common");
    const [content, setContent] = useState<string | null>(null);
    const [failed, setFailed] = useState(false);
    const [attempt, setAttempt] = useState(0);
    const closeRef = useRef(onClose);
    closeRef.current = onClose;
    useEffect(() => {
        let active = true;
        setContent(null); setFailed(false);
        void ReadTodayDoneAnswer(item.id).then(async (answer) => {
            if (!active || answer.id !== item.id) return;
            setContent(answer.content);
            await MarkTodayDoneOpened(item.id);
        }).catch(() => { if (active) setFailed(true); });
        const withdraw = onPagePrivacyWithdrawal(() => { active = false; setContent(null); closeRef.current(); });
        return () => { active = false; withdraw(); };
    }, [item.id, attempt]);
    return <aside data-testid="today-done-answer-panel" className="flex h-full min-h-0 flex-col bg-background-accent">
        <header className="flex items-center justify-between border-b border-border px-4 py-3">
            <h2 className="text-sm font-semibold text-ink">{t("doneForYou.heading")}</h2>
            <Button size="icon" variant="quiet" aria-label={common("close")} onClick={() => measureToday("T21", "open", "opened", onClose)}><X className="size-4" /></Button>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
            {content !== null && <MarkdownContent content={content} />}
            {failed && <button type="button" className="text-sm text-muted" onClick={() => measureToday("T21", "do", "done", () => setAttempt((value) => value + 1))}>{t("doneForYou.loadFailed")}</button>}
        </div>
    </aside>;
}
