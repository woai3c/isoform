import { measureToday, recordTodayAction, type TodayActionOutcome } from "@/lib/today-telemetry";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { FindTimeAnswer } from "@/components/find-time-answer";
import { notifyHoldSettingsChanged, onHoldSettingsChanged } from "@/lib/hold-settings-events";
import { DismissHoldOffer, UndoDismissHoldOffer, GetHoldSettings, GetHoldStatus, GetHoldDayShape, HoldTime, OpenExternalURL, OpenPermissionSettings, ProposeHold, RemoveHold, SetHoldCalendar, UndoHold, UndoRemoveHold, type FindTimeRange, type HoldProposal, type HoldSettings, type HoldStatus, type HoldThing, type TodayDayShape } from "@/lib/wails-shim-app";
import { TodayDayStrip } from "./TodayDayStrip";
import { computerClock, computerHourOptions } from "@/lib/brief-time";
import { useChangeDrivenRead } from "@/hooks/useChangeDrivenRead";
import { holdEventMatches } from "./useDirectHold";

export interface HoldCardSource { thing: HoldThing; due?: string; reason?: string; preferred?: FindTimeRange; lengthMinutes?: number; telemetryAction?: "T14" | "T84" | "T31" | "D01" }

function dateKey(value: string): string {
    const date = new Date(value);
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}

export function HoldCard({ source, onClose }: { source: HoldCardSource; onClose: () => void }) {
    const { t, i18n } = useTranslation("today");
    const track = <T,>(outcome: TodayActionOutcome, run: () => T): T => source.telemetryAction ? measureToday(source.telemetryAction, "reach", outcome, run) : run();
    const [settings, setSettings] = useState<HoldSettings | null>(null);
    const [proposal, setProposal] = useState<HoldProposal | null>(null);
    const [slot, setSlot] = useState<FindTimeRange | null>(null);
    const [shape, setShape] = useState<TodayDayShape | null>(null);
    const [status, setStatus] = useState<HoldStatus | null>(null);
    const [otherTimes, setOtherTimes] = useState(false);
    const [changing, setChanging] = useState(false);
    const [checking, setChecking] = useState(false);
    const [filled, setFilled] = useState(false);
    const [changed, setChanged] = useState(false);
    const [fallbackCalendar, setFallbackCalendar] = useState<{ id: string; title: string; previous: string } | null>(null);
    const [dismissal, setDismissal] = useState<string | null>(null);
    const [error, setError] = useState<"load" | "calendar" | "hold" | "remove" | "restore" | "access" | "edited" | null>(null);
    const [failures, setFailures] = useState({ hold: 0, remove: 0, restore: 0 });
    const [clock, setClock] = useState(Date.now());
    const [settingsVersion, setSettingsVersion] = useState(0);
    const closeRef = useRef(onClose);
    closeRef.current = onClose;
    useEffect(() => onHoldSettingsChanged(() => setSettingsVersion((version) => version + 1)), []);

    useEffect(() => {
        let active = true;
        setSettings(null); setProposal(null); setSlot(null); setStatus(null); setDismissal(null); setFallbackCalendar(null); setError(null); setFilled(false); setChanged(false); setFailures({ hold: 0, remove: 0, restore: 0 });
        void GetHoldSettings().then(async (next) => {
            if (!active) return;
            setSettings(next);
            if (!next.granted || !next.offerHolds || next.browserOnly?.length) return;
            let result: HoldProposal;
            try { result = await ProposeHold(source.thing, source.due ?? "", source.lengthMinutes ?? 0); }
            catch (failure) {
                // An existing hold can still be returned without a default. For a
                // new one, let the person select an available calendar first.
                if (!next.calendarId && next.calendars.length > 0) return;
                throw failure;
            }
            if (!active) return;
            const preferred = source.preferred && result.slots.find((candidate) => candidate.start === source.preferred?.start && candidate.end === source.preferred?.end);
            setProposal(result); setSlot(result.dismissed ? null : preferred || result.slots[0] || null); setStatus(result.existing ?? null);
            setChanged(Boolean(source.preferred && !preferred && !result.existing && !result.dismissed && result.slots.length));
        }).catch(() => { if (active) setError("load"); });
        return () => { active = false; };
    }, [source.thing.title, source.thing.sourceKind, source.thing.sourceId, source.due, source.lengthMinutes, source.preferred?.start, source.preferred?.end, settingsVersion]);

    const displayedStart = status && ["held", "moved"].includes(status.state) ? status.start : slot?.start;
    useEffect(() => {
        if (!displayedStart) { setShape(null); return; }
        let active = true;
        void GetHoldDayShape(dateKey(displayedStart)).then((next) => { if (active) setShape(next); }).catch(() => { if (active) setShape(null); });
        return () => { active = false; };
    }, [displayedStart]);

    useEffect(() => {
        if (!status) return;
        // Time changes the answer only at a window's end: the undo window
        // (writeAfter) and the restore window after a removal (removedAt + 5 s).
        // One timeout at that boundary re-reads the hold; no interval.
        const boundary = status.state === "pending_write" ? Date.parse(status.writeAfter) : status.state === "removed" ? Date.parse(status.removedAt) + 5000 : NaN;
        if (!Number.isFinite(boundary)) return;
        const timer = window.setTimeout(() => {
            setClock(Date.now());
            void GetHoldStatus(status.id).then(setStatus).catch(() => setError("load"));
        }, Math.max(0, boundary - Date.now()) + 50);
        return () => window.clearTimeout(timer);
    }, [status?.id, status?.state, status?.writeAfter, status?.removedAt]);

    // A real hold transition (holds:updated) re-reads this card's hold once; a payload
    // that names another hold is ignored. Always enabled: with no hold yet the read does nothing.
    useChangeDrivenRead(true, ["holds:updated"], () => {
        if (!status) return undefined;
        return GetHoldStatus(status.id).then(setStatus).catch(() => setError("load"));
    }, (data) => holdEventMatches(data, status?.id));

    useEffect(() => {
        if (status?.state === "failed") setFailures((current) => ({ ...current, hold: current.hold + 1 }));
    }, [status?.id, status?.state]);

    useEffect(() => {
        if (status?.state !== "failed" || status.failureReason !== "calendar_unavailable" || !settings) return;
        let active = true;
        void (async () => {
            const current = await GetHoldSettings();
            const choices = current.calendars.filter((calendar) => calendar.id !== status.calendarId);
            const alternative = choices.find((calendar) => calendar.meetingsAccount) ?? choices.find((calendar) => calendar.isDefault) ?? choices[0];
            if (!alternative) return;
            const next = await ProposeHold(source.thing, source.due ?? "", source.lengthMinutes ?? 0);
            if (!active || next.existing || next.dismissed || !next.slots.length) return;
            const original = next.slots.find((candidate) => candidate.start === status.start && candidate.end === status.end);
            const previous = settings.calendars.find((calendar) => calendar.id === status.calendarId)?.title ?? "";
            setProposal(next); setSlot(original ?? next.slots[0]); setChanged(!original);
            setFallbackCalendar({ id: alternative.id, title: alternative.title, previous });
            setSettings(current); setStatus(null); setError(null);
        })().catch(() => { /* Leave the failure visible when no alternative can be checked. */ });
        return () => { active = false; };
    }, [status?.id, status?.state, status?.failureReason]);

    useEffect(() => {
        if (status && ["pending_write", "held", "moved"].includes(status.state) && Date.parse(status.start) <= clock) closeRef.current();
    }, [status?.start, status?.state, clock]);

    useEffect(() => {
        if (!dismissal) return;
        const timer = window.setTimeout(() => closeRef.current(), 5000);
        return () => window.clearTimeout(timer);
    }, [dismissal]);

    const dismiss = async () => {
        if (!proposal) return;
        try { setDismissal(await track("done", () => DismissHoldOffer(proposal.thing))); }
        catch { setError("hold"); }
    };
    const undoDismiss = async () => {
        if (!dismissal) return;
        try { await track("undone", () => UndoDismissHoldOffer(dismissal)); setDismissal(null); }
        catch { onClose(); }
    };
    const when = (range: FindTimeRange) => {
        const start = new Date(range.start), end = new Date(range.end);
        const language = i18n.resolvedLanguage ?? i18n.language;
        const day = new Intl.DateTimeFormat(language, { weekday: "long", hour: "numeric", minute: "2-digit", ...computerHourOptions() }).format(start);
        const endTime = computerClock(end, language);
        return `${day}–${endTime}`;
    };
    const submitHold = async (nextProposal: HoldProposal, selected: FindTimeRange, calendarID: string, currentSettings: HoldSettings, report = true) => {
        const started = performance.now();
        let next: HoldStatus;
        try {
            next = await HoldTime(nextProposal, selected, calendarID);
            if (report && source.telemetryAction) recordTodayAction(source.telemetryAction, "reach", next.state === "calendar_unavailable" || next.state === "conflict" ? "failed" : "done", started);
        } catch (error) {
            if (report && source.telemetryAction) recordTodayAction(source.telemetryAction, "reach", "failed", started);
            throw error;
        }
        if (next.state === "calendar_unavailable") {
            const current = await GetHoldSettings();
            const alternative = current.calendars.find((calendar) => calendar.id === next.calendarId);
            const previous = currentSettings.calendars.find((calendar) => calendar.id === calendarID)?.title ?? "";
            if (alternative) { setFallbackCalendar({ id: alternative.id, title: alternative.title, previous }); setSettings(current); }
            else setError("hold");
        } else if (next.state === "conflict") {
            if (next.start && next.end) { setChanged(false); setFilled(true); setSlot({ start: next.start, end: next.end }); }
            else { setFilled(false); setError("hold"); setOtherTimes(true); }
        } else { setFallbackCalendar(null); setFilled(false); setChanged(false); setStatus(next); }
        return next.state !== "calendar_unavailable" && next.state !== "conflict";
    };
    const hold = async () => {
        if (!proposal || !slot || !settings) return;
        setChecking(true); setError(null);
        try { await submitHold(proposal, slot, fallbackCalendar?.id ?? settings.calendarId, settings); }
        catch { setError("hold"); setFailures((current) => ({ ...current, hold: current.hold + 1 })); }
        finally { setChecking(false); }
    };
    const undo = async () => {
        if (!status) return;
        try { await track("undone", () => UndoHold(status.id)); onClose(); }
        catch { setError("hold"); }
    };
    const remove = async () => {
        if (!status) return;
        setError(null);
        try { await track("done", () => RemoveHold(status.id)); setStatus(await GetHoldStatus(status.id)); }
        catch (failure) {
            if (String(failure).includes("HOLD_CALENDAR_EDITED")) { setError("edited"); return; }
            try { const current = await GetHoldSettings(); setError(current.granted ? "remove" : "access"); if (current.granted) setFailures((previous) => ({ ...previous, remove: previous.remove + 1 })); }
            catch { setError("remove"); setFailures((previous) => ({ ...previous, remove: previous.remove + 1 })); }
        }
    };
    const restore = async () => {
        if (!status) return;
        setError(null);
        try { await track("undone", () => UndoRemoveHold(status.id)); setStatus(await GetHoldStatus(status.id)); }
        catch { setError("restore"); setFailures((current) => ({ ...current, restore: current.restore + 1 })); }
    };
    const retry = async () => {
        if (!settings) return;
        const started = performance.now();
        let outcome: TodayActionOutcome = "failed";
        setChecking(true); setError(null);
        try {
            const next = await ProposeHold(source.thing, source.due ?? "", source.lengthMinutes ?? 0);
            const selected = next.slots.find((candidate) => candidate.start === slot?.start && candidate.end === slot?.end);
            setProposal(next); setStatus(next.existing ?? null); setOtherTimes(false);
            if (next.existing || next.dismissed) { outcome = "opened"; return; }
            if (!selected) {
                setSlot(next.slots[0] ?? null);
                setFilled(false); setChanged(next.slots.length > 0);
                if (next.slots.length === 0) setError("hold");
                return;
            }
            setSlot(selected); setFilled(false); setChanged(false);
            if (await submitHold(next, selected, settings.calendarId, settings, false)) outcome = "done";
        } catch { setError("hold"); setFailures((current) => ({ ...current, hold: current.hold + 1 })); }
        finally { setChecking(false); if (source.telemetryAction) recordTodayAction(source.telemetryAction, "reach", outcome, started); }
    };
    const chooseCalendar = async (id: string) => {
        setError(null);
        try { await track("done", () => SetHoldCalendar(id)); setSettings((current) => current ? { ...current, calendarId: id } : current); setChanging(false); notifyHoldSettingsChanged(); }
        catch { setError("calendar"); }
    };
    if (settings && (!settings.granted || !settings.offerHolds || settings.browserOnly?.length)) return <FindTimeAnswer />;
    if (dismissal && proposal) return <p role="status" className="text-[13px] text-muted">{t("holds.notNowMessage", { title: proposal.thing.title })} · <button type="button" className="text-accent" onClick={() => void undoDismiss()}>{t("holds.undo")}</button></p>;
    if (proposal?.dismissed) return <p role="status" className="text-[13px] text-muted">{t("holds.notNow")}</p>;
    if (!settings) return <p role="status">{t(error ? "holds.noCalendar" : "holds.checking")}</p>;
    if (error === "load") return <p role="status">{t("holds.noCalendar")}</p>;
    if (!settings.calendarId && !status) return settings.calendars.length === 0 ? <p role="status">{t("holds.noCalendar")}</p> : (
        <label className="block text-[13px] text-muted">
            {t("holds.chooseCalendar")}
            <select aria-label={t("holds.calendarLabel")} value="" onChange={(event) => void chooseCalendar(event.target.value)} className="mt-2 block rounded-md border border-border bg-background px-2 py-1 text-sm">
                <option value="" disabled>{t("holds.chooseCalendar")}</option>
                {[...new Set(settings.calendars.map((item) => item.sourceTitle))].map((account) => <optgroup key={account} label={account}>{settings.calendars.filter((item) => item.sourceTitle === account).map((item) => <option key={item.id} value={item.id}>{item.title}</option>)}</optgroup>)}
            </select>
            {error === "calendar" && <span role="status" className="mt-2 block text-destructive">{t("holds.calendarFailed")}</span>}
        </label>
    );
    if (!proposal) return <p role="status">{t("holds.checking")}</p>;
    if (!slot) return <p role="status">{t("holds.noTimes")}</p>;
    const current = status && ["held", "moved"].includes(status.state) ? { start: status.start, end: status.end } : slot;
    const calendar = settings.calendars.find((item) => item.id === (status?.calendarId ?? settings.calendarId));
    const sourceKey = ({ brief: "sourceBrief", task: "sourceTask", promise: "sourcePromise", week: "sourceWeek", up_next: "sourceUpNext" } as const)[source.thing.sourceKind as "brief" | "task" | "promise" | "week" | "up_next"];
    const due = source.due ? new Date(source.due) : null;
    const reason = source.reason || (due && Number.isFinite(due.getTime()) ? t("holds.due", { when: new Intl.DateTimeFormat(i18n.resolvedLanguage ?? i18n.language, { weekday: "long", month: "short", day: "numeric" }).format(due) }) : "");
    const removed = status?.state === "removed" || status?.state === "gone";
    const showUndoRemove = status?.state === "removed" && status.removedAt && clock < Date.parse(status.removedAt) + 5000;
    const holdPreview = status && ["pending_write", "held", "moved"].includes(status.state) ? "held" : "proposed";
    const strip = shape && !removed ? { ...shape, events: shape.events.filter((event) => !status?.id || event.holdId !== status.id), hold: { start: current.start, end: current.end, label: t("holds.dayHold") } } : shape ? { ...shape, hold: null } : null;
    const failureKind = error === "hold" || status?.state === "failed" ? "hold" : error === "remove" ? "remove" : error === "restore" ? "restore" : null;
    const reportFailure = status?.failureReason !== "unconfirmed" && failureKind !== null && failures[failureKind] >= 2;
    return (
        <div className="rounded-xl border border-border bg-background p-4" data-testid="hold-card">
            {strip && <TodayDayStrip shape={strip} requestedDate={dateKey(current.start)} holdPreview={holdPreview} holdTitle={proposal.thing.title} onRemoveHold={status && ["held", "moved"].includes(status.state) && error !== "access" ? () => void remove() : undefined} />}
            <p className="mt-3 text-[14px] font-semibold text-ink">{removed ? t(status?.state === "gone" ? "holds.gone" : "holds.removed") : status && ["held", "moved"].includes(status.state) ? t("holds.onYourCalendar", { when: when(current) }) : fallbackCalendar ? t("holds.calendarUnavailable", { calendar: fallbackCalendar.previous, other: fallbackCalendar.title }) : filled ? t("holds.slotFilled", { when: when(current) }) : changed ? t("holds.slotChanged", { when: when(current) }) : t("holds.question", { when: when(current), title: proposal.thing.title })}</p>
            {!removed && (reason || sourceKey) && <p className="mt-2 text-[13px] text-muted">{reason}{reason && sourceKey ? " · " : ""}{sourceKey && t(`holds.${sourceKey}`)}</p>}
            {status?.state === "moved" && <p className="mt-2 text-[13px] text-muted">{t("holds.moved", { when: when(current) })}</p>}
            {!removed && calendar && <p className="mt-2 text-[13px] text-muted">{t("holds.onCalendar", { calendar: calendar.title })} {calendar.meetingsAccount && t("holds.sharedBusy")} {(!status || status.state === "failed") && <button type="button" className="text-accent" onClick={() => track("opened", () => setChanging(!changing))}>{t("holds.change")}</button>}</p>}
            {changing && <select aria-label={t("holds.calendarLabel")} value={settings.calendarId} onChange={(event) => void chooseCalendar(event.target.value)} className="mt-2 rounded-md border border-border bg-background px-2 py-1 text-sm">{[...new Set(settings.calendars.map((item) => item.sourceTitle))].map((account) => <optgroup key={account} label={account}>{settings.calendars.filter((item) => item.sourceTitle === account).map((item) => <option key={item.id} value={item.id}>{item.title}</option>)}</optgroup>)}</select>}
            {checking && <p role="status" className="mt-3 text-[13px] text-accent">{t("holds.checking")}</p>}
            {error && <p role="status" className="mt-3 text-[13px] text-destructive">{t(reportFailure ? "holds.stillFailed" : error === "calendar" ? "holds.calendarFailed" : error === "hold" ? "holds.failed" : error === "remove" ? "holds.removeFailed" : error === "access" ? "holds.accessOff" : error === "edited" ? "holds.edited" : "holds.restoreFailed")}</p>}
            {error === "access" && <div className="mt-3 flex gap-2"><Button size="sm" variant="outline" onClick={() => void track("opened", () => OpenPermissionSettings("calendars"))}>{t("holds.openSettings")}</Button><Button size="sm" variant="quiet" onClick={() => void track("opened", () => OpenExternalURL("/System/Applications/Calendar.app"))}>{t("holds.openCalendar")}</Button></div>}
            {(error === "remove" || error === "edited") && <Button className="mt-3" size="sm" variant="quiet" onClick={() => void track("opened", () => OpenExternalURL("/System/Applications/Calendar.app"))}>{t("holds.openCalendar")}</Button>}
            {error === "restore" && <Button className="mt-3" size="sm" variant="outline" onClick={() => void restore()}>{t("holds.retry")}</Button>}
            {!checking && !status && <div className="mt-3 flex flex-wrap gap-2"><Button size="sm" onClick={() => void hold()}>{error === "hold" ? t("holds.retry") : fallbackCalendar ? t("holds.holdOn", { calendar: fallbackCalendar.title }) : t("holds.hold", { when: when(slot) })}</Button>{!fallbackCalendar && proposal.slots.length > 1 && <Button size="sm" variant="outline" onClick={() => track("opened", () => setOtherTimes(!otherTimes))}>{t("holds.otherTimes")}</Button>}<Button size="sm" variant="quiet" onClick={() => void dismiss()}>{t("holds.notNow")}</Button></div>}
            {otherTimes && !status && <div className="mt-2 flex flex-wrap gap-2">{proposal.slots.filter((choice) => choice.start !== slot.start || choice.end !== slot.end).map((choice) => <Button key={choice.start} size="sm" variant="outline" onClick={() => track("done", () => setSlot(choice))}>{when(choice)}</Button>)}</div>}
            {status?.state === "checking" && <p role="status" className="mt-3 text-[13px] text-accent">{t("holds.checking")}</p>}
            {status?.state === "pending_write" && <p className="mt-3 text-[13px] text-accent">{t("holds.held")} · <button type="button" onClick={() => void undo()}>{t("holds.undo")}</button></p>}
            {status && ["held", "moved"].includes(status.state) && error !== "access" && <Button className="mt-3" size="sm" variant="outline" onClick={() => void remove()}>{t(error === "remove" ? "holds.retry" : "holds.remove")}</Button>}
            {showUndoRemove && <button type="button" className="mt-3 text-[13px] text-accent" onClick={() => void restore()}>{t("holds.undo")}</button>}
            {status?.state === "failed" && (status.failureReason === "unconfirmed" ? <div className="mt-3"><p role="status" className="text-[13px] text-destructive">{t("holds.unconfirmed")}</p><Button size="sm" variant="quiet" onClick={() => void track("opened", () => OpenExternalURL("/System/Applications/Calendar.app"))}>{t("holds.openCalendar")}</Button></div> : <p role="status" className="mt-3 text-[13px] text-destructive">{t(reportFailure ? "holds.stillFailed" : "holds.failed")} <button type="button" className="text-accent" onClick={() => void retry()}>{t("holds.retry")}</button></p>)}
        </div>
    );
}
