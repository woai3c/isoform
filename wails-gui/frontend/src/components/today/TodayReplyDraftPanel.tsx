import { measureToday } from "@/lib/today-telemetry";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { SheetTitle } from "@/components/ui/sheet";
import { CancelTodayReplyDraft, OpenTodayReplyDraft } from "@/lib/wails-shim-app";
import { TaskThreadPane } from "@/components/tasks/TaskThreadPane";
import { TodayNewMessageDraft } from "./TodayNewMessageDraft";

/** newMessage: the card has no Mail thread to reply to; Go proved the
 *  person's address from a record before the chip was offered. */
export type TodayReplyDraftRequest = { briefId: string; cardId: string; message: string; label: string; id: number; newMessage?: boolean };

export function TodayReplyDraftPanel(props: { request: TodayReplyDraftRequest; onClose: () => void; sheet?: boolean }) {
    return props.request.newMessage ? <NewMessageDraftPanel {...props} /> : <ThreadReplyDraftPanel {...props} />;
}

function NewMessageDraftPanel({ request, onClose, sheet = false }: { request: TodayReplyDraftRequest; onClose: () => void; sheet?: boolean }) {
    const { t } = useTranslation("today");
    return <aside className="flex h-full min-h-0 flex-col border-l border-border-subtle bg-background-accent" data-testid="today-reply-draft-panel">
        {sheet && <SheetTitle className="sr-only">{t("replyDraft.newTitle")}</SheetTitle>}
        <header className="flex items-center justify-between border-b border-border px-5 py-3">
            <h2 className="text-sm font-semibold text-ink">{t("replyDraft.newTitle")}</h2>
            <Button size="icon" variant="quiet" aria-label={t("topicPanel.close")} onClick={() => measureToday("T12", "open", "opened", onClose)}><X className="size-4" /></Button>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto p-5"><TodayNewMessageDraft request={request} /></div>
    </aside>;
}

function ThreadReplyDraftPanel({ request, onClose, sheet = false }: {
    request: TodayReplyDraftRequest;
    onClose: () => void;
    sheet?: boolean;
}) {
    const { t } = useTranslation("today");
    const [task, setTask] = useState<string | null>(null);
    const [failed, setFailed] = useState(false);
    const [retry, setRetry] = useState(0);
    const [stopped, setStopped] = useState(false);
    const [stopFailed, setStopFailed] = useState(false);
    const [stopping, setStopping] = useState(false);
    const stop = useRef<() => void>(() => {});
    useEffect(() => {
        let active = true;
        let mounted = true;
        let finished = false;
        let succeeded = false;
        let canceling = false;
        let failedWhileStopping = false;
        const runId = typeof crypto !== "undefined" && "randomUUID" in crypto
            ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
        setTask(null);
        setFailed(false);
        setStopped(false);
        setStopFailed(false);
        setStopping(false);
        stop.current = () => {
            if (!active || finished) return;
            canceling = true;
            setStopping(true);
            void CancelTodayReplyDraft(runId).then(() => {
                if (!active || succeeded) return;
                active = false;
                setStopped(true);
            }).catch(() => {
                if (active) {
                    setStopFailed(true);
                    if (failedWhileStopping) setFailed(true);
                }
            }).finally(() => { canceling = false; if (mounted) setStopping(false); });
        };
        void OpenTodayReplyDraft(request.briefId, request.cardId, request.message, request.label, runId).then((id) => {
            finished = true;
            succeeded = true;
            if (active) setTask(id);
        }).catch(() => {
            finished = true;
            if (active && canceling) failedWhileStopping = true;
            else if (active) {
                setFailed(true);
            }
        });
        return () => {
            if (active && !finished) void CancelTodayReplyDraft(runId).catch(() => {});
            active = false;
            mounted = false;
        };
    }, [request, retry]);
    return <aside className="flex h-full min-h-0 flex-col border-l border-border-subtle bg-background-accent" data-testid="today-reply-draft-panel">
        {sheet && <SheetTitle className="sr-only">{t("replyDraft.title")}</SheetTitle>}
        <header className="flex items-center justify-between border-b border-border px-5 py-3">
            <h2 className="text-sm font-semibold text-ink">{t("replyDraft.title")}</h2>
            <div className="flex items-center gap-1">
                {!task && !failed && !stopped && <Button size="sm" variant="quiet" disabled={stopping} onClick={() => measureToday("T12", "do", "stopped", () => stop.current())}>{t("replyDraft.stop")}</Button>}
                <Button size="icon" variant="quiet" aria-label={t("topicPanel.close")} onClick={() => measureToday("T12", "open", "opened", onClose)}><X className="size-4" /></Button>
            </div>
        </header>
        {stopFailed && <p role="alert" className="px-5 pt-3 text-sm text-error">{t("replyDraft.stopFailed")}</p>}
        {task ? <TaskThreadPane id={task} onClose={onClose} telemetryAction="T12" /> : <div className="p-5">
            <div className="space-y-3 rounded-xl border border-border bg-panel p-4" aria-busy={!failed && !stopped}>
                {failed || stopped ? <div role="alert" className="space-y-2 text-sm text-muted">
                    <p>{t(stopped ? "replyDraft.stopped" : "replyDraft.failed")}</p>
                    <Button size="sm" variant="quiet" onClick={() => measureToday("T12", "do", "done", () => setRetry((n) => n + 1))}>{t("todayBrief.retry")}</Button>
                </div> : <div role="status" aria-label={t("replyDraft.writing")} className="space-y-3">
                    <span className="block h-3 w-1/3 rounded bg-surface-active" />
                    <span className="block h-3 w-2/3 rounded bg-surface-active" />
                    <span className="block h-3 w-full rounded bg-surface-active" />
                    <span className="block h-3 w-4/5 rounded bg-surface-active" />
                </div>}
                {!failed && !stopped && <div className="flex gap-2"><Button size="sm" disabled>{t("replyDraft.send")}</Button><Button size="sm" variant="secondary" disabled>{t("replyDraft.edit")}</Button></div>}
            </div>
        </div>}
    </aside>;
}
