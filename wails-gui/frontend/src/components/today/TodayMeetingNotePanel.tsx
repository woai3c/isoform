import { measureToday } from "@/lib/today-telemetry";
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { SheetTitle } from "@/components/ui/sheet";
import { useMainChatPanel } from "@/hooks/useMainChatPanel";
import { onPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";
import { GetTodayMeetingNote, SendTodayMeetingNote, type TodayBeforeYouTalk, type TodayMeetingNote } from "@/lib/wails-shim-app";
import type { TodayChatContextRef } from "@/lib/today-chat-navigation";

export type TodayMeetingNoteRequest = { meeting: TodayBeforeYouTalk; prompt: string; id: number };
type SendState = { timer?: number; status: "waiting" | "sending" | "sent" | "failed" | "unknown" };
const sends = new Map<string, SendState>();
const listeners = new Set<() => void>();
const publish = () => listeners.forEach((listener) => listener());
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
onPagePrivacyWithdrawal(() => {
    for (const [id, send] of sends) if (send.timer) { window.clearTimeout(send.timer); sends.delete(id); }
    publish();
});
function queueSend(ref: TodayChatContextRef, id: string, body: string) {
    if (sends.get(id)?.status === "waiting" || sends.get(id)?.status === "sending") return;
    const pending: SendState = { status: "waiting" };
    pending.timer = window.setTimeout(() => {
        sends.set(id, { status: "sending" }); publish();
        void SendTodayMeetingNote(ref, id, body).then(() => {
            sends.set(id, { status: "sent" }); publish();
        }).catch((error: unknown) => {
            const message = error instanceof Error ? error.message : String(error);
            sends.set(id, { status: message.includes("outcome is unknown") || message.includes("receipt pending") ? "unknown" : "failed" }); publish();
        });
    }, 5000);
    sends.set(id, pending); publish();
}
function undoSend(id: string) {
    const pending = sends.get(id);
    if (!pending?.timer) return;
    window.clearTimeout(pending.timer); sends.delete(id); publish();
}

export function TodayMeetingNotePanel({ request, onClose, sheet = false }: { request: TodayMeetingNoteRequest; onClose: () => void; sheet?: boolean }) {
    const { t } = useTranslation("today");
    const chat = useMainChatPanel(true);
    const ref = useMemo<TodayChatContextRef>(() => ({ sourceKind: "calendar", sourceId: request.meeting.meetingId, meetingNote: true, meetingStartMs: request.meeting.startAt, generatedTitle: request.meeting.meetingTitle, generatedPeople: [request.meeting.personName] }), [request]);
    const [note, setNote] = useState<TodayMeetingNote | null>(null);
    const [status, setStatus] = useState<"loading" | "writing" | "ready" | "failed" | "stopped">("loading");
    const [started, setStarted] = useState(false);
    const [retry, setRetry] = useState(0);
    const [body, setBody] = useState("");
    const [editing, setEditing] = useState(false);
    const [stopFailed, setStopFailed] = useState(false);
    const finished = useRef(false);
    const generation = useRef(0);
    const id = note?.answer.answerItemId ?? "";
    const send = useSyncExternalStore(subscribe, () => sends.get(id), () => undefined);

    useEffect(() => {
        const turn = ++generation.current;
        finished.current = false;
        setNote(null); setStatus("loading"); setStarted(false); setEditing(false); setStopFailed(false);
        void GetTodayMeetingNote(ref).then(async (saved) => {
            if (generation.current !== turn) return;
            if (saved) { setNote(saved); setBody(saved.answer.answer); setStatus("ready"); return; }
            setStatus("writing");
            const outcome = await chat.send(request.prompt, ref, true);
            if (generation.current !== turn) return;
            if (outcome === "failed") setStatus("failed");
            else setStarted(true);
        }).catch(() => { if (generation.current === turn) setStatus("failed"); });
        return () => { generation.current++; };
        // The request is a single click; send is stable for this panel.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [ref, request.prompt, retry]);

    useEffect(() => {
        if (!started || chat.waiting || chat.pending || !chat.lastUserItemId || finished.current || chat.stopped) return;
        finished.current = true;
        const turn = generation.current;
        void GetTodayMeetingNote(ref).then((saved) => {
            if (generation.current !== turn) return;
            if (!saved) { setStatus("failed"); return; }
            setNote(saved); setBody(saved.answer.answer); setStatus("ready");
        }).catch(() => { if (generation.current === turn) setStatus("failed"); });
    }, [started, chat.waiting, chat.pending, chat.lastUserItemId, chat.stopped, ref]);

    const source = chat.items.findIndex((item) => item.id === chat.lastUserItemId && item.kind === "user");
    const live = source < 0 ? "" : chat.items.slice(source + 1).filter((item) => item.kind === "assistant").map((item) => item.content ?? "").join("");
    const sent = note?.state === "sent" || send?.status === "sent" || send?.status === "waiting" || send?.status === "sending";
    const unknown = note?.state === "unknown" || send?.status === "unknown";
    return <aside className="flex h-full min-h-0 flex-col border-l border-border-subtle bg-background-accent" data-testid="today-meeting-note-panel">
        {sheet && <SheetTitle className="sr-only">{t("meetingNote.title")}</SheetTitle>}
        <header className="flex items-center justify-between border-b border-border px-5 py-3">
            <h2 className="text-sm font-semibold text-ink">{t("meetingNote.title")}</h2>
            <div className="flex items-center gap-1">
                {status === "writing" && chat.waiting && chat.runId && <Button size="sm" variant="quiet" onClick={() => { setStopFailed(false); void measureToday("T43", "do", "stopped", chat.stop).then(() => setStatus("stopped")).catch(() => setStopFailed(true)); }}>{t("replyDraft.stop")}</Button>}
                <Button size="icon" variant="quiet" aria-label={t("topicPanel.close")} onClick={() => measureToday("T43", "open", "opened", onClose)}><X className="size-4" /></Button>
            </div>
        </header>
        {stopFailed && <p role="alert" className="px-5 pt-3 text-sm text-error">{t("replyDraft.stopFailed")}</p>}
        <div className="min-h-0 flex-1 overflow-y-auto p-5">
            <div className="space-y-3 rounded-xl border border-border bg-panel p-4 text-sm text-ink" aria-busy={status === "loading" || status === "writing"}>
                <h3 className="text-xs font-semibold uppercase text-muted">{t("meetingNote.mailHeader")}</h3>
                <p className="text-xs text-muted">{t("meetingNote.why", { name: request.meeting.personName })}</p>
                <p className="text-xs text-muted">{t("meetingNote.to", { name: request.meeting.personName, address: note?.address ?? "" })}</p>
                <p className="text-xs text-muted">{t("meetingNote.subject", { subject: note?.subject ?? request.meeting.meetingTitle })}</p>
                {editing && note && !sent && !unknown ? <textarea aria-label={t("meetingNote.body")} className="min-h-32 w-full rounded-lg border border-border bg-background p-2 text-ink" value={body} onChange={(event) => setBody(event.target.value)} />
                    : (body || live) ? <p className="whitespace-pre-wrap">{body || live}</p> : (status === "loading" || status === "writing") && <p role="status" className="text-muted">{t("replyDraft.writing")}</p>}
                {(status === "failed" || status === "stopped") && <p role="alert" className="text-error">{t(status === "stopped" ? "replyDraft.stopped" : "replyDraft.failed")} <Button size="sm" variant="quiet" onClick={() => measureToday("T43", "do", "done", () => setRetry((n) => n + 1))}>{t("todayBrief.retry")}</Button></p>}
                {sent ? <div className="flex items-center gap-2 text-muted"><span>{t("meetingNote.sent", { name: note?.name ?? request.meeting.personName })}</span>{send?.status === "waiting" && <Button size="sm" variant="quiet" onClick={() => measureToday("T43", "reach", "undone", () => undoSend(id))}>{t("meetingNote.undo")}</Button>}</div>
                    : unknown ? <p role="alert" className="text-error">{t("meetingNote.unknown")}</p>
                    : <div className="flex gap-2"><Button size="sm" disabled={!note || status !== "ready" || !body.trim()} onClick={() => measureToday("T43", "reach", "done", () => note && queueSend(ref, id, body))}>{t("replyDraft.send")}</Button><Button size="sm" variant="secondary" disabled={!note || status !== "ready"} onClick={() => measureToday("T43", "open", "opened", () => setEditing((value) => !value))}>{t(editing ? "meetingNote.done" : "replyDraft.edit")}</Button></div>}
                {send?.status === "failed" && <p role="alert" className="text-error">{t("meetingNote.sendFailed")}</p>}
            </div>
        </div>
    </aside>;
}
