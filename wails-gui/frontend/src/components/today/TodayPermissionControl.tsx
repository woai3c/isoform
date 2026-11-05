import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { GetTodayPermissionStates, OpenPermissionSettings, RecordTodayAction, RequestTodayPermission, type TodayPermissionStates } from "@/lib/wails-shim-app";
import { useChangeDrivenRead } from "@/hooks/useChangeDrivenRead";
import { Button } from "@/components/ui/button";

type PermissionKey = "screen_recording" | "calendars";

/** A permission is probed without prompting; only the person's click asks. */
export function TodayPermissionControl({ permission, action, explain = false, bothMissing = false, inline = false, onChanged }: { permission: PermissionKey; action: string; explain?: boolean; bothMissing?: boolean; inline?: boolean; onChanged?: () => void }) {
    const { t } = useTranslation("today");
    const [states, setStates] = useState<TodayPermissionStates | null>(null);
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    const refresh = useCallback(async () => {
        try { setStates(await GetTodayPermissionStates()); setFailed(false); }
        catch { setStates(null); setFailed(true); }
    }, []);
    // One read on mount, one per permissions:changed, and one when the window is
    // shown again (the shared rule; no focus listener of its own).
    useChangeDrivenRead(true, ["permissions:changed"], refresh);
    const state = permission === "screen_recording" ? states?.screenRecording : states?.calendars;
    const act = async () => {
        if (busy) return;
        if (!state) { await refresh(); return; }
        setBusy(true); setFailed(false);
        const started = performance.now();
        let outcome: "done" | "opened" | "failed" = "done";
        try {
            if (state === "denied") { await OpenPermissionSettings(permission); outcome = "opened"; }
            else { await RequestTodayPermission(permission); await refresh(); onChanged?.(); }
        } catch { outcome = "failed"; setFailed(true); }
        finally {
            setBusy(false);
            void RecordTodayAction("today", action, state === "denied" ? "open" : "do", outcome, Math.max(0, Math.round(performance.now() - started))).catch(() => {});
        }
    };
    const line = permission === "calendars" ? "todayBrief.blindCalendarLine" : state === "denied" ? "todayBrief.blindScreenDenied" : bothMissing ? "todayBrief.blindBothLine" : "todayBrief.blindScreenLine";
    const label = t(state === "denied" ? "todayBrief.permissionSettings" : "todayBrief.permissionTurnOn");
    const disabled = busy || (!state && !failed);
    return <div className={explain ? "space-y-2.5" : "shrink-0"}>
        {explain && <p className="text-[13px] leading-relaxed text-muted">{t(line)}{inline && <> · <button type="button" className="font-medium text-accent" disabled={disabled} onClick={() => void act()}>{label}</button></>}</p>}
        {explain && !inline ? <Button size="sm" disabled={disabled} onClick={() => void act()}>{label}</Button> : !explain ? <button type="button" className="text-[12px] font-medium text-accent" disabled={disabled} onClick={() => void act()}>{label}</button> : null}
        {failed && <p role="alert" className="text-xs text-error">{t("todayBrief.permissionFailed")}</p>}
    </div>;
}
