import { measureToday, recordTodayAction, type TodayActionKind, type TodayActionOutcome } from "@/lib/today-telemetry";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Check, FileText, MoreHorizontal } from "lucide-react";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { AIReady, AbortHandoff, ActOnDesktopTask, DeleteHandoff, GetDesktopTaskThread, ReplyToDesktopTask, type DesktopTaskAction, type DesktopTaskThread } from "@/lib/wails-shim-app";
import { onPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { taskOutcomeLine } from "./task-outcome";
import { FailureLine } from "./FailureLine";
import { measureTasks, recordTasksAction, type TasksActionId } from "@/lib/tasks-telemetry";
import { beginRestart, openYourAI, stopEventText, undoRestart, usePendingRestart } from "./task-stop";
import { computerClock, computerHourOptions } from "@/lib/brief-time";

type PendingSend = { at: number; timer?: number; failed?: "definitive" | "unknown" | "receipt" };
const sends = new Map<string, PendingSend>();
const listeners = new Set<() => void>();
function publish() { listeners.forEach((listener) => listener()); }
function subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; }
function sendDraft(id: string, body: string) {
    const pending: PendingSend = { at: Date.now() };
    pending.timer = window.setTimeout(() => {
        pending.timer = undefined;
        sends.set(id, { at: pending.at }); publish();
        void ActOnDesktopTask(id, { action: "send", text: body }).then(() => {
            sends.delete(id); publish();
        }).catch((error: unknown) => {
            const message = error instanceof Error ? error.message : String(error);
            const failed = message.includes("did not say whether it sent") || message.includes("outcome is unknown") ? "unknown" : message.includes("receipt pending") ? "receipt" : "definitive";
            sends.set(id, { at: pending.at, failed }); publish();
        });
    }, 5000);
    sends.set(id, pending); publish();
}
function undoDraft(id: string) {
    const pending = sends.get(id);
    if (!pending?.timer) return;
    window.clearTimeout(pending.timer); sends.delete(id); publish();
}
onPagePrivacyWithdrawal(() => {
    for (const [id, pending] of sends) {
        if (pending.timer) { window.clearTimeout(pending.timer); sends.delete(id); }
    }
    publish();
});

/** One task, one view (Tasks K2): the thread beside the board. onDoIt runs
 *  a waiting task's start after "Started · Undo"; onGone is a link to a
 *  deleted task; onDeleted follows ··· > Delete task. */
export function TaskThreadPane({ id, onClose, onOpened, onDoIt, onGone, onDeleted, renderSupplement, telemetryAction, tasksMeasured }: { id: string; onClose?: () => void; onOpened?: () => void; onDoIt?: () => Promise<unknown>; onGone?: () => void; onDeleted?: () => void; renderSupplement?: (detail: DesktopTaskThread | null) => ReactNode; telemetryAction?: "D01" | "T12" | "T80"; /** Opened beside Tasks: measures the H rows. */ tasksMeasured?: boolean }) {
    const track = <T,>(kind: TodayActionKind, outcome: TodayActionOutcome, fn: () => T): T => telemetryAction ? measureToday(telemetryAction, kind, outcome, fn) : fn();
    const { t, i18n } = useTranslation("app");
    const [detail, setDetail] = useState<DesktopTaskThread | null>(null);
    const [failed, setFailed] = useState(false);
    const [actionFailed, setActionFailed] = useState(false);
    const [busy, setBusy] = useState(false);
    const [reply, setReply] = useState("");
    const [editing, setEditing] = useState(false);
    const [body, setBody] = useState("");
    const [renaming, setRenaming] = useState(false);
    const [title, setTitle] = useState("");
    const generation = useRef(0);
    const privacyEpoch = useRef(0);
    const initialized = useRef(false);
    const openedRef = useRef(onOpened);
    openedRef.current = onOpened;
    const goneRef = useRef(onGone);
    goneRef.current = onGone;
    const [checksOpen, setChecksOpen] = useState(false);
    const [confirmDelete, setConfirmDelete] = useState(false);
    const previousPending = useRef<PendingSend | undefined>();
    const pending = useSyncExternalStore(subscribe, () => sends.get(id), () => undefined);
    const restart = usePendingRestart(id);
    const [restartFailed, setRestartFailed] = useState(false);
    const [aiReady, setAIReady] = useState(false);
    const noAssistant = detail?.stopCause === "no_assistant";
    useEffect(() => {
        if (!noAssistant) return;
        let active = true;
        void Promise.resolve().then(() => AIReady()).then((ready) => { if (active) setAIReady(!!ready); }).catch(() => {});
        return () => { active = false; };
    }, [noAssistant]);
    const measureH = <T,>(row: TasksActionId, kind: TodayActionKind, outcome: TodayActionOutcome, fn: () => T): T => tasksMeasured ? measureTasks(row, kind, outcome, fn) : fn();
    const restartFailedH = () => { if (tasksMeasured) recordTasksAction("H01", "do", "failed"); setRestartFailed(true); };
    const restartedH = () => { if (tasksMeasured) recordTasksAction("H01", "do", "done"); void load(); };
    const startAgain = () => { setRestartFailed(false); beginRestart(id, restartFailedH, restartedH); };
    const load = useCallback(async () => {
        const request = ++generation.current;
        try { const result = await GetDesktopTaskThread(id); if (request === generation.current) { setDetail(result); setFailed(false); if (!initialized.current) { initialized.current = true; setBody(result.draft?.body ?? ""); setTitle(result.title); openedRef.current?.(); } } }
        catch (error) {
            if (request !== generation.current) return;
            if ((error instanceof Error ? error.message : String(error)).includes("task_gone")) { goneRef.current?.(); return; }
            setDetail(null); setFailed(true);
        }
    }, [id]);
    useEffect(() => {
        let active = true;
        initialized.current = false; previousPending.current = undefined;
        setDetail(null); setFailed(false); setReply(""); setEditing(false); setActionFailed(false);
        void load();
        if (!id.startsWith("draft.")) void Promise.resolve().then(() => ActOnDesktopTask(id, { action: "opened" })).catch(() => {});
        const off = EventsOn("handoff:updated", () => { if (active) void load(); });
        const withdraw = onPagePrivacyWithdrawal(() => { privacyEpoch.current++; generation.current++; initialized.current = false; setDetail(null); setBody(""); setReply(""); setBusy(false); if (active) void load(); });
        const timer = window.setInterval(() => { if (active) void load(); }, 5000);
        return () => { active = false; privacyEpoch.current++; generation.current++; off?.(); withdraw(); window.clearInterval(timer); };
    }, [id, load]);
    useEffect(() => { if (previousPending.current && !pending) void load(); previousPending.current = pending; }, [pending, load]);
    const act = async (input: DesktopTaskAction) => {
        if (busy) return;
        const started = performance.now();
        const epoch = privacyEpoch.current;
        generation.current++; setBusy(true); setActionFailed(false);
        try { const next = await ActOnDesktopTask(id, input); if (telemetryAction) recordTodayAction(telemetryAction, "do", "done", started); if (epoch === privacyEpoch.current) { generation.current++; setDetail(next); if (input.action === "not_now") onClose?.(); } }
        catch { if (telemetryAction) recordTodayAction(telemetryAction, "do", "failed", started); if (epoch === privacyEpoch.current) setActionFailed(true); }
        finally { if (epoch === privacyEpoch.current) setBusy(false); }
    };
    const clock = (value?: string) => {
        if (!value) return "";
        const date = new Date(value);
        return Number.isNaN(date.getTime()) ? "" : computerClock(date, i18n.language);
    };
    const when = (value?: string) => {
        if (!value) return "";
        const date = new Date(value);
        if (Number.isNaN(date.getTime())) return "";
        return date.toDateString() === new Date().toDateString() ? clock(value) : date.toLocaleString(i18n.language, { weekday: "short", hour: "numeric", minute: "2-digit", ...computerHourOptions() });
    };
    const origin = detail?.origin;
    const originLine = origin && ({
        up_next: () => t("taskThread.originUpNext", { time: clock(origin.at) }),
        brief_chip: () => t("taskThread.originBrief", { time: clock(origin.slot || origin.at), label: origin.label }),
        chat: () => t("taskThread.originChat", { time: when(origin.at) }),
        list: () => t("taskThread.originList", { time: clock(origin.at) }),
        routine: () => t("taskThread.originRoutine", { day: new Date(origin.slot || origin.at).toLocaleDateString(i18n.language, { weekday: "long" }), time: clock(origin.slot || origin.at) }),
    } as Record<string, () => string>)[origin.kind]?.();
    const runnerName = detail?.runner === "codex" ? t("taskThread.aiCodex") : t("taskThread.aiClaudeCode");
    const outcomeLine = taskOutcomeLine(detail?.outcome, i18n.language, t);
    const draft = detail?.draft;
    const person = draft?.recipients?.to?.join(", ") || draft?.to || "";
    const who = draft?.recipients && (draft.recipients.cc?.length
        ? t("taskThread.copying", { to: person, cc: draft.recipients.cc.join(", ") })
        : draft.recipients.notIncluded?.length
          ? t(draft.recipients.notIncluded.length === 1 ? "taskThread.notCopiedOne" : "taskThread.notCopiedMany", { to: person, others: draft.recipients.notIncluded.join(", ") })
          : t("taskThread.onlyTo", { to: person }));
    return <section className="flex min-h-0 flex-1 flex-col bg-background-accent" data-testid="task-thread">
        {!detail ? <div className="p-5 text-sm text-muted">{failed ? <><p role="alert">{t("taskThread.failed")} <Button variant="quiet" size="sm" onClick={() => void track("do", "done", load)}>{t("taskThread.retry")}</Button></p></> : t("taskThread.loading")}{renderSupplement?.(null)}</div> : <>
            <header className="flex items-start gap-2 border-b border-border px-5 py-4">
                <div className="min-w-0 flex-1">{renaming ? <form onSubmit={(event) => { event.preventDefault(); void act({ action: "rename", title }).then(() => setRenaming(false)); }}><input aria-label={t("taskThread.rename")} value={title} onChange={(event) => setTitle(event.target.value)} className="w-full rounded border border-border bg-panel p-2 text-ink" /><Button size="sm" type="submit">{t("taskThread.save")}</Button></form> : <h2 className="text-lg font-semibold text-ink">{detail.title}</h2>}
                <p className="text-xs text-muted">{detail.group === "needs_you" && detail.stopCause ? t("taskThread.needsCouldntFinish") : detail.group === "done" ? t("taskThread.doneAt", { time: when(detail.outcome?.at || detail.updatedAt) }) : detail.group === "running" ? detail.progress?.total ? t("taskThread.runningStep", { step: Math.min(detail.progress.done + 1, detail.progress.total), total: detail.progress.total }) : t("taskThread.runningStarting") : t("taskThread.needsUpdated", { time: when(detail.updatedAt) })}</p></div>
                {(detail.actions.length > 0 || !id.startsWith("draft.")) && <DropdownMenu><DropdownMenuTrigger asChild><Button variant="quiet" size="icon" aria-label={t("taskThread.more")} onClick={() => track("open", "opened", () => undefined)}><MoreHorizontal className="size-4" /></Button></DropdownMenuTrigger><DropdownMenuContent align="end">
                    {detail.actions.includes("mark_done") && <DropdownMenuItem onSelect={() => void act({ action: "mark_done" })}>{t("taskThread.markDone")}</DropdownMenuItem>}
                    {detail.actions.includes("rename") && <DropdownMenuItem onSelect={() => track("open", "opened", () => { setTitle(detail.title); setRenaming(true); })}>{t("taskThread.rename")}</DropdownMenuItem>}
                    {detail.actions.includes("not_now") && <DropdownMenuItem onSelect={() => void act({ action: "not_now" })}>{t("taskThread.notNow")}</DropdownMenuItem>}
                    {detail.actions.includes("stop") && <DropdownMenuItem className="text-error" onSelect={() => void track("do", "stopped", () => measureH("H02", "do", "stopped", () => AbortHandoff(id))).then(load).catch(() => setActionFailed(true))}>{t("taskThread.stop")}</DropdownMenuItem>}
                    {!id.startsWith("draft.") && <DropdownMenuItem className="text-error" onSelect={() => setConfirmDelete(true)}>{t("taskThread.deleteTask")}</DropdownMenuItem>}
                </DropdownMenuContent></DropdownMenu>}
            </header>
            <div className="flex-1 space-y-3 overflow-y-auto px-5 py-4">
                {outcomeLine && <p data-testid="task-outcome" className={detail.outcome?.kind === "stopped" ? "rounded-lg bg-surface-active p-3 text-sm text-muted" : "rounded-lg bg-surface-active p-3 text-sm text-ink"}>{outcomeLine}</p>}
                {originLine && <p className="text-center text-xs text-muted">{originLine}</p>}
                {detail.plan && (detail.plan.unavailable
                    ? <p data-testid="task-plan-unavailable" className="text-sm text-muted">{t("taskThread.planUnavailable")} <Button variant="quiet" size="sm" onClick={() => void load()}>{t("taskThread.retry")}</Button></p>
                    : <div data-testid="task-plan" className="max-w-[90%] space-y-2 rounded-xl border border-border bg-panel p-3 text-sm text-ink">
                        <div><p className="text-xs font-semibold text-muted">{t("taskThread.planWill")}</p><p>{detail.plan.will}</p></div>
                        {detail.plan.wont && <div><p className="text-xs font-semibold text-muted">{t("taskThread.planWont")}</p><p>{detail.plan.wont}</p></div>}
                        {detail.plan.need && <div><p className="text-xs font-semibold text-muted">{t("taskThread.planNeed")}</p><p>{detail.plan.need}</p></div>}
                    </div>)}
                {detail.plan && <p data-testid="task-who" className="text-xs text-muted">{t("taskThread.whoDoesIt", { who: runnerName })}</p>}
                {detail.plan && detail.project && <p data-testid="task-project" className="text-xs text-muted">{t("taskThread.inProject", { project: detail.project })}</p>}
                {(detail.files?.length ?? 0) > 0 && <div data-testid="task-files" className="flex flex-wrap gap-1.5">{detail.files!.map((file) => <span key={file} title={file} className="inline-flex max-w-full items-center gap-1 rounded-full border border-border bg-panel px-2.5 py-1 text-xs text-ink"><FileText className="size-3.5 shrink-0 text-muted" /><span className="truncate">{file.split("/").pop()}</span></span>)}</div>}
                {detail.plan && !restart && <div data-testid="task-plan-actions" className="flex gap-2">
                    <Button size="sm" disabled={busy || !onDoIt || !!detail.plan.unavailable} onClick={() => { if (!onDoIt) return; setRestartFailed(false); beginRestart(id, restartFailedH, restartedH, onDoIt); }}>{t("tasks.doIt")}</Button>
                    {detail.actions.includes("not_now") && <Button size="sm" variant="secondary" disabled={busy} onClick={() => void act({ action: "not_now" })}>{t("taskThread.notNow")}</Button>}
                </div>}
                {detail.checks && (detail.checks.unverifiable
                    ? <div data-testid="task-checks" className="space-y-2"><p className="text-sm text-muted">{t("taskThread.unverifiable")}</p><div className="flex gap-2">
                        {detail.actions.includes("mark_done") && <Button size="sm" disabled={busy} onClick={() => void act({ action: "mark_done" })}>{t("taskThread.markDone")}</Button>}
                        <Button size="sm" variant="secondary" onClick={startAgain}>{t("taskThread.startAgain")}</Button></div></div>
                    : <div data-testid="task-checks" className="space-y-1.5">
                        <p className="text-sm text-copper">{t("taskThread.checksLine", { met: detail.checks.met, total: detail.checks.total })} · <button type="button" className="underline-offset-2 hover:underline" aria-expanded={checksOpen} onClick={() => setChecksOpen(!checksOpen)}>{checksOpen ? t("taskThread.hide") : t("taskThread.seeWhich")}</button></p>
                        {checksOpen && <ul className="space-y-1 text-sm text-ink">{detail.checks.criteria.map((criterion, index) => <li key={index} className="flex items-start gap-1.5">{detail.checks!.unmet[index] ? <span>{t("taskThread.couldntConfirm", { criterion })}</span> : <><Check aria-hidden className="mt-0.5 size-3.5 shrink-0 text-muted" /><span>{criterion}</span></>}</li>)}</ul>}
                    </div>)}
                {detail.events?.map((event) => <div key={event.id} className={event.kind === "you" ? "ml-auto max-w-[85%] text-right" : "max-w-[90%]"}>
                    {event.file ? <p className="rounded-lg border border-border bg-panel p-3 text-sm text-ink">{event.file.name}</p> : <p className={event.kind === "line" ? "text-xs text-muted" : "whitespace-pre-wrap rounded-xl border border-border bg-panel p-3 text-sm text-ink"}>{(event.code && stopEventText(event.code, event.params, clock(event.at), t)) || event.text}</p>}
                    {event.at && event.code !== "started_again" && <p className="mt-1 text-xs text-muted">{clock(event.at)}</p>}
                    {!event.chosenId && event.choices?.map((choice) => <Button key={choice.id} className="mr-2 mt-2" size="sm" variant="secondary" disabled={busy} onClick={() => void act({ action: "choose", eventId: event.id, choiceId: choice.id })}>{choice.label}</Button>)}
                </div>)}
                {restart ? <p data-testid="task-restart" className="flex items-center gap-2 text-xs text-muted">{t("taskThread.started")}{restart.timer !== 0 && <Button variant="quiet" size="sm" onClick={() => undoRestart(id)}>{t("taskThread.undo")}</Button>}</p>
                : detail.group === "needs_you" && detail.stopCause ? <div data-testid="task-stop-actions" className="flex gap-2">
                    {noAssistant && <Button size="sm" variant={aiReady ? "secondary" : "default"} onClick={openYourAI}>{t("taskThread.setUp")}</Button>}
                    {detail.stopCause === "signed_out" && <Button size="sm" onClick={openYourAI}>{t("taskThread.signIn")}</Button>}
                    <Button size="sm" variant={(noAssistant && !aiReady) || detail.stopCause === "signed_out" ? "secondary" : "default"} onClick={startAgain}>{t("taskThread.startAgain")}</Button>
                </div>
                : detail.stoppedBy === "person" && <p data-testid="task-stopped-line" className="flex items-center gap-1 text-xs text-muted">{t("taskThread.stopped")} ·<Button variant="quiet" size="sm" onClick={startAgain}>{t("taskThread.startOver")}</Button></p>}
                {restartFailed && <FailureLine tone="copper" text={t("taskThread.startFailed")} onRetry={() => detail.plan && onDoIt ? beginRestart(id, restartFailedH, restartedH, onDoIt) : startAgain()} />}
                {detail.steps?.length > 0 && <ul className="space-y-2 rounded-xl border border-border bg-panel p-3 text-sm text-ink">{detail.steps.map((step, index) => <li key={index}>{step.text}</li>)}</ul>}
                {draft && (pending && !pending.failed || draft.state === "sent" ? <div className="flex items-center gap-2 rounded-xl border border-border bg-panel p-3 text-sm text-ink"><span className="flex-1">{t("taskThread.sent", { person, time: clock(pending ? new Date(pending.at).toISOString() : draft.sentAt) })}</span>{pending?.timer && <Button variant="quiet" size="sm" onClick={() => track("reach", "undone", () => undoDraft(id))}>{t("taskThread.undo")}</Button>}</div> : draft.state === "not_sent" ? <p className="rounded-xl border border-border bg-panel p-3 text-sm text-muted">{t("taskThread.notSent", { person })}</p> : <div data-testid="task-draft-card" className="space-y-3 rounded-xl border border-border bg-panel p-4 text-sm text-ink">
                    <h3 className="text-xs font-semibold uppercase text-muted">{draft.channel === "slack" ? t("taskThread.slackHeader") : t("taskThread.mailHeader")}</h3>
                    {draft.why && <div className="rounded-lg bg-surface-active p-3"><p className="text-xs font-semibold">{t("taskThread.why")}</p><p>{draft.why.textLocalized?.[i18n.language] || draft.why.text}</p><p className="mt-1 text-xs text-muted">{t("tasks.evidenceCard.source", { source: draft.why.source === "mail" ? t("tasks.evidenceCard.mail") : draft.why.source, when: when(draft.why.at) })}</p></div>}
                    <p><span className="text-muted">{t("taskThread.to")}: </span>{draft.to}</p>{draft.subject && <p><span className="text-muted">{t("taskThread.subject")}: </span>{draft.subject}</p>}
                    {editing ? <textarea aria-label={t("taskThread.edit")} className="min-h-36 w-full rounded-lg border border-border bg-panel p-2" value={body} onChange={(event) => setBody(event.target.value)} /> : <p className="whitespace-pre-wrap">{draft.body}</p>}
                    {who && <p className="text-xs text-muted">{who}</p>}
                    {(actionFailed || pending?.failed) && <FailureLine tone="copper" text={pending?.failed === "unknown" ? t("taskThread.sendUnknown") : pending?.failed === "receipt" ? t("taskThread.sendReceipt") : t("taskThread.actionFailed")} />}
                    <div className="flex gap-2">{detail.actions.includes("send") && <Button size="sm" disabled={busy || !body.trim() || pending?.failed === "unknown" || pending?.failed === "receipt"} onClick={() => track("reach", "done", () => { sendDraft(id, editing ? body : ""); setEditing(false); })}>{t("taskThread.send")}</Button>}<Button size="sm" variant="secondary" onClick={() => track("open", "opened", () => { setBody(draft.body); setEditing(!editing); })}>{editing ? t("taskThread.cancel") : t("taskThread.edit")}</Button></div>
                </div>)}
                {actionFailed && !draft && <FailureLine tone="copper" text={t("taskThread.actionFailed")} />}
                {renderSupplement?.(detail)}
            </div>
            {!id.startsWith("draft.") && detail.stopCause !== "loop_guard" && <form className="flex gap-2 border-t border-border p-3" onSubmit={(event) => { event.preventDefault(); if (!reply.trim() || busy) return; const epoch = privacyEpoch.current; generation.current++; setBusy(true); setActionFailed(false); void track("do", "sent", () => ReplyToDesktopTask(id, reply)).then((next) => { if (epoch === privacyEpoch.current) { generation.current++; setDetail(next); setReply(""); } }).catch(() => { if (epoch === privacyEpoch.current) setActionFailed(true); }).finally(() => { if (epoch === privacyEpoch.current) setBusy(false); }); }}>
                <input className="min-w-0 flex-1 rounded-lg border border-border bg-panel px-3 py-2 text-sm text-ink" placeholder={t("taskThread.reply")} aria-label={t("taskThread.reply")} value={reply} onChange={(event) => setReply(event.target.value)} />
                <Button type="submit" size="sm" disabled={busy || !reply.trim()}>{t("taskThread.send")}</Button>
            </form>}
            <ConfirmDialog open={confirmDelete} onOpenChange={setConfirmDelete} title={t("taskThread.deleteTask")} description={t("taskThread.deleteConfirm", { title: detail.title })} confirmLabel={t("taskThread.delete")} variant="danger" onConfirm={() => { setConfirmDelete(false); void measureH("H04", "do", "done", () => DeleteHandoff(id)).then(() => onDeleted?.()).catch(() => setActionFailed(true)); }} />
        </>}
    </section>;
}
