import { measureToday, recordTodayAction } from "@/lib/today-telemetry";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { AddTodayNoticedSchedule, UndoTodayNoticedSchedule, type MobileBriefActivityTarget, type TodayNoticedSchedule } from "@/lib/wails-shim-app";

/** Accept the suggestion here; only Edit navigates to Activity. */
export function TodayNoticedAction({ target, label, onEdit, primary }: {
    target: MobileBriefActivityTarget;
    label: string;
    onEdit: (target: MobileBriefActivityTarget) => void;
    primary: boolean;
}) {
    const { t } = useTranslation("today");
    const [saved, setSaved] = useState<TodayNoticedSchedule | null>(null);
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    const perform = async () => {
        if (busy) return;
        const started = performance.now();
        const action = target.action === "follow_up" ? "N03" : "N04";
        setBusy(true); setFailed(false);
        try { setSaved(await AddTodayNoticedSchedule(target)); recordTodayAction(action, "do", "done", started); }
        catch { setFailed(true); recordTodayAction(action, "do", "failed", started); }
        finally { setBusy(false); }
    };
    const undo = async () => {
        if (!saved || busy) return;
        const started = performance.now();
        const action = target.action === "follow_up" ? "N03" : "N04";
        setBusy(true); setFailed(false);
        try { await UndoTodayNoticedSchedule(target, saved); setSaved(null); recordTodayAction(action, "do", "undone", started); }
        catch { setFailed(true); recordTodayAction(action, "do", "failed", started); }
        finally { setBusy(false); }
    };
    return <span className={`inline-flex flex-wrap items-center gap-1 text-[13px] ${saved ? "text-accent" : "text-muted"}`} data-testid="today-noticed-action">
        {saved ? <>
            <span>{t("noticedAction.added")}</span><span aria-hidden>{"·"}</span>
            <Button variant="quiet" size="sm" className="text-accent" disabled={busy} onClick={() => void undo()}>{t("noticedAction.undo")}</Button><span aria-hidden>{"·"}</span>
            <Button variant="quiet" size="sm" className="text-accent" disabled={busy} onClick={() => measureToday(target.action === "follow_up" ? "N03" : "N04", "open", "opened", () => onEdit({ ...target, schedule: saved }))}>{t("noticedAction.edit")}</Button>
        </> : <Button size="sm" variant={primary ? "default" : "secondary"} disabled={busy} onClick={() => void perform()}>{label}</Button>}
        {failed && <span role="alert" className="inline-flex items-center gap-1 text-copper">
            {t(saved ? "noticedAction.undoFailed" : target.action === "follow_up" ? "noticedAction.followUpFailed" : "noticedAction.routineFailed")}{" "}
            <Button variant="quiet" size="sm" disabled={busy} onClick={() => void (saved ? undo() : perform())}>{t("todayBrief.retry")}</Button>
        </span>}
    </span>;
}
