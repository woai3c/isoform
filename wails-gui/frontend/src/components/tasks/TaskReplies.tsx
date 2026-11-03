import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Check, CircleHelp } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "@/components/ui/context-menu";
import { cn } from "@/lib/utils";
import type { DesktopTaskEffectStep, DesktopTaskReply, DesktopTaskThread } from "@/lib/wails-shim-app";

/** Where a task's replies stand: read by a running task, or waiting on a stop. */
export type ReplyPhase = "running" | "stopped_by_you" | "stopped_by_isoform" | "other";
export function replyPhase(detail: DesktopTaskThread): ReplyPhase {
    if (detail.group === "running") return "running";
    if (detail.stoppedBy === "person") return "stopped_by_you";
    if (detail.group === "needs_you" && detail.stopCause) return "stopped_by_isoform";
    return "other";
}
export const repliesNeedingChoice = (detail: DesktopTaskThread) => (detail.replies ?? []).filter((reply) => reply.state === "uncertain" && !reply.choice).length;
export const stepsNeedingAnswer = (detail: DesktopTaskThread) => (detail.effectSteps ?? []).filter((step) => step.proof === "unknown" && !step.answer).length;
/** A step's plain name, or what it acted on. */
export function stepLabel(step: DesktopTaskEffectStep): string {
    if (step.name) return step.name;
    const target = step.target ?? "";
    return step.kind === "write_file" ? target.split("/").pop() || target : target;
}

/** A link inside a reply's gray line, in the accent color. */
function LineLink({ children, onClick, label, disabled }: { children: ReactNode; onClick: () => void; label?: string; disabled?: boolean }) {
    return <button type="button" aria-label={label} disabled={disabled} className="text-accent hover:underline disabled:opacity-50" onClick={onClick}>{children}</button>;
}
function Line({ parts, tone = "gray" }: { parts: ReactNode[]; tone?: "gray" | "copper" }) {
    return <p data-testid="reply-line" className={cn("mt-1 text-xs", tone === "copper" ? "text-copper" : "text-muted")}>{parts.map((part, index) => <span key={index}>{index > 0 && " · "}{part}</span>)}</p>;
}

export type ReplyAction = "reply_undo" | "reply_leave_out" | "reply_use" | "reply_use_again" | "reply_change";

/** One of the person's replies with the line under it (v1.0.5–v1.0.7). */
export function ReplyMessage({ reply, phase, clock, busy, alreadyPickedUp, onAct, onUndo }: { reply: DesktopTaskReply; phase: ReplyPhase; clock: (value?: string) => string; busy: boolean; alreadyPickedUp: boolean; onAct: (action: ReplyAction) => void; onUndo: () => void }) {
    const { t } = useTranslation("app");
    const stopped = phase === "stopped_by_you" || phase === "stopped_by_isoform";
    const pickedUp = t("taskThread.replyPickedUp", { time: clock(reply.pickedUpAt) });
    const leaveOut = <LineLink label={t("taskThread.replyLeaveOutLabel")} disabled={busy} onClick={() => onAct("reply_leave_out")}>{t("taskThread.replyLeaveOut")}</LineLink>;
    let line: ReactNode = null;
    let dim = reply.state !== "picked_up" || !!reply.leftOut;
    if (alreadyPickedUp) line = <Line parts={[t("taskThread.replyAlreadyPickedUp")]} />;
    else if (reply.state === "uncertain" && stopped) {
        dim = true;
        line = reply.choice === "left_out" ? <Line parts={[t("taskThread.replyLeftOut"), <LineLink disabled={busy} onClick={() => onAct("reply_change")}>{t("taskThread.replyChange")}</LineLink>]} />
            : reply.choice === "use_again" ? <Line parts={[t("taskThread.replyUsingAgain"), <LineLink disabled={busy} onClick={() => onAct("reply_change")}>{t("taskThread.replyChange")}</LineLink>]} />
            : <div data-testid="reply-uncertain"><Line tone="copper" parts={[t("taskThread.replyMayHaveActed")]} /><div className="mt-2 flex justify-end gap-2">
                <Button size="sm" variant="secondary" aria-label={t("taskThread.replyLeaveOutLabel")} disabled={busy} onClick={() => onAct("reply_leave_out")}>{t("taskThread.replyLeaveOut")}</Button>
                <Button size="sm" variant="secondary" disabled={busy} onClick={() => onAct("reply_use_again")}>{t("taskThread.replyUseAgain")}</Button>
            </div></div>;
    } else if (reply.leftOutOfRun) line = <Line parts={[t("taskThread.replyLeftOutOfRun")]} />;
    else if (stopped && reply.leftOut) line = <Line parts={[t("taskThread.replyLeftOut"), <LineLink disabled={busy} onClick={() => onAct("reply_use")}>{t("taskThread.replyUseIt")}</LineLink>]} />;
    else if (stopped && reply.state === "picked_up") line = <Line parts={[pickedUp, leaveOut]} />;
    else if (stopped && reply.state === "waiting") line = <Line parts={[phase === "stopped_by_you" ? t("taskThread.replyGoesInStartOver") : t("taskThread.replyGoesInStartAgain"), leaveOut]} />;
    else if (reply.state === "waiting" && phase === "running") line = <Line parts={[t("taskThread.replyWaiting"), <LineLink disabled={busy} onClick={onUndo}>{t("taskThread.undo")}</LineLink>]} />;
    else if (reply.state === "picked_up") line = <Line parts={[pickedUp]} />;
    const bubble = <p data-testid="task-reply" className={cn("whitespace-pre-wrap rounded-xl border border-border bg-panel p-3 text-left text-sm text-ink", dim && "opacity-60")}>{reply.text}</p>;
    const waiting = reply.state === "waiting" && phase === "running" && !alreadyPickedUp;
    return <div className="ml-auto max-w-[85%] text-right" data-reply-id={reply.id}>
        <ContextMenu>
            <ContextMenuTrigger asChild>{bubble}</ContextMenuTrigger>
            <ContextMenuContent>
                <ContextMenuItem onSelect={() => void navigator.clipboard?.writeText(reply.text)}>{t("taskThread.replyCopy")}</ContextMenuItem>
                {waiting && <ContextMenuItem onSelect={onUndo}>{t("taskThread.undo")}</ContextMenuItem>}
            </ContextMenuContent>
        </ContextMenu>
        {line}
    </div>;
}

/** A reply that didn't save: dimmed, its words kept, the failure in copper. */
export function FailedReply({ text, onRetry, onEdit }: { text: string; onRetry: () => void; onEdit: () => void }) {
    const { t } = useTranslation("app");
    return <div className="ml-auto max-w-[85%] text-right" data-testid="reply-failed">
        <p className="whitespace-pre-wrap rounded-xl border border-border bg-panel p-3 text-left text-sm text-ink opacity-60">{text}</p>
        <Line tone="copper" parts={[t("taskThread.replyDidntSend"), <LineLink onClick={onRetry}>{t("taskThread.retry")}</LineLink>, <LineLink onClick={onEdit}>{t("taskThread.edit")}</LineLink>]} />
    </div>;
}

export type StepAction = "step_skip" | "step_redo";

/** Isoform's message about steps that may already have happened (v1.0.2 §1). */
export function EffectStepsMessage({ steps, clock, busy, onAnswer, onCheck }: { steps: DesktopTaskEffectStep[]; clock: (value?: string) => string; busy: boolean; onAnswer: (step: DesktopTaskEffectStep, action: StepAction) => void; onCheck?: (step: DesktopTaskEffectStep) => void }) {
    const { t } = useTranslation("app");
    const checkLink = (step: DesktopTaskEffectStep) => {
        if (!step.app) return null;
        const web = !!step.target && /^https?:\/\//.test(step.target);
        const label = web ? t("taskThread.stepCheckOn", { place: step.app }) : t("taskThread.stepCheckIn", { app: step.app === "Finder" ? t("taskThread.appFinder") : step.app });
        if (!onCheck || (step.app !== "Finder" && !web)) return <span>{label}</span>;
        return <button type="button" data-testid="step-check-link" className="text-accent underline-offset-2 hover:underline" onClick={() => onCheck(step)}>{label}</button>;
    };
    const done = steps.filter((step) => step.proof === "done");
    const unknown = steps.filter((step) => step.proof === "unknown");
    return <>
        {done.map((step) => <p key={step.id} data-testid="step-already-done" className="flex items-start gap-1.5 text-xs text-muted"><Check aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" /><span>{t("taskThread.stepAlreadyDone", { step: stepLabel(step) })}{step.app && <> · {checkLink(step)}</>}</span></p>)}
        {unknown.length > 0 && <div data-testid="task-effect-steps" className="max-w-[90%] space-y-2 rounded-xl border border-border bg-panel p-3 text-sm text-ink">
            <p>{unknown.length === 1 ? t("taskThread.stepsMayHaveHappenedOne") : t("taskThread.stepsMayHaveHappenedMany", { n: unknown.length })}</p>
            {unknown.map((step) => <div key={step.id} data-testid="effect-step-card" className="space-y-2 rounded-lg border border-border p-3">
                <p className="flex items-start gap-1.5"><CircleHelp aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-copper" /><span>{stepLabel(step)}</span></p>
                <p className="text-xs text-muted">{clock(step.at)}{step.app && <>{clock(step.at) && " · "}{checkLink(step)}</>}</p>
                <div className="flex gap-2">
                    <Button size="sm" variant="secondary" aria-pressed={step.answer === "skip"} className={cn(step.answer === "skip" && "border-border-strong font-semibold")} disabled={busy} onClick={() => onAnswer(step, "step_skip")}>{t("taskThread.stepSkip")}</Button>
                    <Button size="sm" variant="secondary" aria-pressed={step.answer === "redo"} className={cn(step.answer === "redo" && "border-border-strong font-semibold")} disabled={busy} onClick={() => onAnswer(step, "step_redo")}>{t("taskThread.stepRedo")}</Button>
                </div>
            </div>)}
        </div>}
    </>;
}

export type ThreadEvent = NonNullable<DesktopTaskThread["events"]>[number];
type ThreadStep = DesktopTaskThread["steps"][number];
export type ThreadItem =
    | { kind: "event"; event: ThreadEvent }
    | { kind: "reply"; reply: DesktopTaskReply }
    | { kind: "step"; step: ThreadStep; index: number }
    | { kind: "effects" };

/**
 * The thread as one timeline (tasks-review v1.0.12): finished steps under the
 * message that introduced them or at the time they were confirmed, replies at
 * the time they were picked up, the stop's step cards under the stop message,
 * then the live steps and the waiting replies last.
 */
export function threadTimeline(detail: DesktopTaskThread, stopped: boolean): { items: ThreadItem[]; live: ThreadStep[]; waiting: DesktopTaskReply[] } {
    const time = (value?: string) => {
        const ms = value ? Date.parse(value) : NaN;
        return Number.isNaN(ms) ? undefined : ms;
    };
    const events = detail.events ?? [];
    const replies = detail.replies ?? [];
    const byId = new Map(replies.map((reply) => [reply.id, reply]));
    const eventIds = new Set(events.map((event) => event.id));
    const waiting = replies.filter((reply) => reply.state === "waiting").sort((a, b) => (time(a.sentAt) ?? 0) - (time(b.sentAt) ?? 0));
    const waitingIds = new Set(waiting.map((reply) => reply.id));
    const cards = stopped && (detail.effectSteps?.length ?? 0) > 0;
    const lastStopId = events.filter((event) => event.code === "stopped").pop()?.id;
    const entries: { at: number; seq: number; item: ThreadItem }[] = [];
    let last = 0;
    const push = (at: number | undefined, item: ThreadItem) => {
        const when = at ?? last;
        last = Math.max(last, when);
        entries.push({ at: when, seq: entries.length, item });
    };
    for (const event of events) {
        const reply = byId.get(event.id);
        if (reply && waitingIds.has(reply.id)) continue;
        push(reply ? time(reply.pickedUpAt) ?? time(event.at) : time(event.at), reply ? { kind: "reply", reply } : { kind: "event", event });
        if (cards && event.id === lastStopId) push(time(event.at), { kind: "effects" });
    }
    for (const reply of replies) {
        if (!eventIds.has(reply.id) && !waitingIds.has(reply.id)) push(time(reply.pickedUpAt) ?? time(reply.sentAt), { kind: "reply", reply });
    }
    const intro = events.find((event) => event.kind === "isoform");
    const introAt = time(intro?.at) ?? time(detail.origin?.at);
    const live: ThreadStep[] = [];
    (detail.steps ?? []).forEach((step, index) => {
        if (step.state === "done") push(time(step.at) ?? introAt, { kind: "step", step, index });
        else live.push(step);
    });
    if (cards && !lastStopId) push(Number.MAX_SAFE_INTEGER, { kind: "effects" });
    entries.sort((a, b) => a.at - b.at || a.seq - b.seq);
    return { items: entries.map((entry) => entry.item), live, waiting };
}
