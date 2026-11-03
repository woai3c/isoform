import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { GetTodayObservationPauseSince, RecordTodayAction, ResumeObservation } from "@/lib/wails-shim-app";
import { computerClock } from "@/lib/brief-time";

/** Resume the existing observation pause without leaving Today. */
export function TodayPauseLine() {
    const { t, i18n } = useTranslation("today");
    const [since, setSince] = useState(0);
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    useEffect(() => {
        let active = true;
        void GetTodayObservationPauseSince().then((value) => { if (active) setSince(value); }).catch(() => {});
        return () => { active = false; };
    }, []);
    const resume = async () => {
        if (busy) return;
        setBusy(true); setFailed(false);
        const started = performance.now();
        let outcome: "done" | "failed" = "done";
        try { await ResumeObservation(); } catch { outcome = "failed"; setFailed(true); }
        finally {
            setBusy(false);
            void RecordTodayAction("today", "T87", "do", outcome, Math.max(0, Math.round(performance.now() - started))).catch(() => {});
        }
    };
    const time = since > 0 ? computerClock(new Date(since), i18n.resolvedLanguage) : "";
    return <div data-testid="today-pause-line" className="text-[12px] leading-snug text-muted">
        <span>{time ? t("captured.pausedSince", { time }) : t("captured.pausedUnknown")}</span>{" · "}
        <button type="button" className="text-accent" disabled={busy} onClick={() => void resume()}>{t("captured.resume")}</button>
        {failed && <p role="alert" className="text-error">{t("captured.resumeFailed")}</p>}
    </div>;
}
