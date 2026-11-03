import { measureToday } from "@/lib/today-telemetry";
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { useMainChatPanel } from "@/hooks/useMainChatPanel";
import { GetTodayBriefDraft, SendTodayBriefDraft, type TodayBriefDraft } from "@/lib/wails-shim-app";
import { onPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";
import type { TodayChatContextRef } from "@/lib/today-chat-navigation";
import type { TodayReplyDraftRequest } from "./TodayReplyDraftPanel";

type PendingSend = { timer?: number; status: "waiting" | "sending" | "sent" | "failed" | "unknown" };
const sends = new Map<string, PendingSend>();
const listeners = new Set<() => void>();
const publish = () => listeners.forEach((listener) => listener());
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
onPagePrivacyWithdrawal(() => {
    for (const [id, send] of sends) if (send.timer) { window.clearTimeout(send.timer); sends.delete(id); }
    publish();
});
function queueSend(ref: TodayChatContextRef, id: string, subject: string, body: string) {
    if (sends.get(id)?.status === "waiting" || sends.get(id)?.status === "sending") return;
    const pending: PendingSend = { status: "waiting" };
    pending.timer = window.setTimeout(() => {
        sends.set(id, { status: "sending" }); publish();
        void SendTodayBriefDraft(ref, id, subject, body).then(() => {
            sends.set(id, { status: "sent" }); publish();
        }).catch((error: unknown) => {
            const message = String(error);
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

/** A complete draft to someone with no Mail thread. The address and its
 *  source line come from a record Go proves; Isoform writes the subject and
 *  the person may edit it. Only Send sends. */
export function TodayNewMessageDraft({ request }: { request: TodayReplyDraftRequest }) {
    const { t, i18n } = useTranslation("today");
    const chat = useMainChatPanel(true);
    const ref = useMemo<TodayChatContextRef>(() => ({ sourceKind: "brief_card", sourceId: request.cardId, briefId: request.briefId, generatedMessage: request.message, briefDraft: true }), [request]);
    const [draft, setDraft] = useState<TodayBriefDraft | null>(null);
    const [status, setStatus] = useState<"loading" | "writing" | "ready" | "failed" | "stopped">("loading");
    const [started, setStarted] = useState(false);
    const [body, setBody] = useState("");
    const [subject, setSubject] = useState("");
    const [editing, setEditing] = useState(false);
    const [stopFailed, setStopFailed] = useState(false);
    const [retry, setRetry] = useState(0);
    const finished = useRef(false);
    const generation = useRef(0);
    const id = draft?.answer.answerItemId ?? "";
    const send = useSyncExternalStore(subscribe, () => sends.get(id), () => undefined);
    const accept = (saved: TodayBriefDraft) => { setDraft(saved); setBody(saved.answer.answer); setSubject(saved.subject || saved.route.subject); setStatus("ready"); };
    useEffect(() => {
        const turn = ++generation.current;
        finished.current = false;
        setDraft(null); setBody(""); setSubject(""); setStatus("loading"); setStarted(false); setEditing(false); setStopFailed(false);
        void GetTodayBriefDraft(ref).then(async (saved) => {
            if (turn !== generation.current) return;
            if (saved && saved.answer.question === request.message) { accept(saved); return; }
            setStatus("writing");
            const result = await chat.send(request.message, ref, true);
            if (turn !== generation.current) return;
            if (result === "failed") setStatus("failed"); else setStarted(true);
        }).catch(() => { if (turn === generation.current) setStatus("failed"); });
        return () => { generation.current++; };
        // A request identifies one click; send stays stable for the panel.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [ref, request.message, retry]);
    useEffect(() => {
        if (!started || chat.waiting || chat.pending || !chat.lastUserItemId || finished.current || chat.stopped) return;
        finished.current = true;
        const turn = generation.current;
        void GetTodayBriefDraft(ref).then((saved) => {
            if (turn !== generation.current) return;
            if (!saved || saved.answer.question !== request.message) { setStatus("failed"); return; }
            accept(saved);
        }).catch(() => { if (turn === generation.current) setStatus("failed"); });
    }, [started, chat.waiting, chat.pending, chat.lastUserItemId, chat.stopped, ref, request.message]);
    const sendStatus = send?.status ?? draft?.state ?? "ready";
    const route = draft?.route;
    const language = i18n.resolvedLanguage ?? i18n.language;
    const sourceLine = route ? route.source === "mail" && route.sourceAt
        ? t("replyDraft.toFromMail", { name: route.name, address: route.address, date: new Intl.DateTimeFormat(language, { month: "short", day: "numeric" }).format(new Date(route.sourceAt)) })
        : t("replyDraft.toFromContacts", { name: route.name, address: route.address }) : "";
    const source = chat.items.findIndex((item) => item.id === chat.lastUserItemId && item.kind === "user");
    const live = source < 0 ? "" : chat.items.slice(source + 1).filter((item) => item.kind === "assistant").map((item) => item.content ?? "").join("");
    const editable = editing && !!draft && (sendStatus === "ready" || sendStatus === "failed");
    return <div className="space-y-3 rounded-xl border border-border bg-panel p-4 text-sm text-ink" aria-busy={status === "loading" || status === "writing"} data-testid="new-message-draft">
        <div className="flex items-center justify-between gap-2">
            <p className="text-xs text-muted">{t("mainChatPanel.fromBrief")}</p>
            {status === "writing" && chat.waiting && chat.runId && <Button size="sm" variant="quiet" onClick={() => { setStopFailed(false); void measureToday("T12", "do", "stopped", chat.stop).then(() => setStatus("stopped")).catch(() => setStopFailed(true)); }}>{t("replyDraft.stop")}</Button>}
        </div>
        {sourceLine && <p className="text-xs text-muted">{sourceLine}</p>}
        {editable ? <label className="block text-xs text-muted">{t("meetingNote.subjectLabel")}<input aria-label={t("meetingNote.subjectLabel")} className="mt-1 block w-full rounded-lg border border-border bg-background p-2 text-ink" value={subject} onChange={(event) => setSubject(event.target.value)} /></label>
            : draft && <p className="text-xs text-muted">{t("meetingNote.subject", { subject })}</p>}
        {stopFailed && <p role="alert" className="text-error">{t("replyDraft.stopFailed")}</p>}
        {editable ? <textarea aria-label={t("meetingNote.body")} className="min-h-32 w-full rounded-lg border border-border bg-background p-2 text-ink" value={body} onChange={(event) => setBody(event.target.value)} />
            : (body || live) ? <p className="whitespace-pre-wrap">{body || live}</p> : (status === "loading" || status === "writing") && <p role="status" className="text-muted">{t("replyDraft.writing")}</p>}
        {(status === "failed" || status === "stopped") && <p role="alert" className="text-error">{t(status === "stopped" ? "replyDraft.stopped" : "replyDraft.failed")} <Button size="sm" variant="quiet" onClick={() => measureToday("T12", "do", "done", () => setRetry((value) => value + 1))}>{t("todayBrief.retry")}</Button></p>}
        {sendStatus === "waiting" || sendStatus === "sending" || sendStatus === "sent" ? <div className="flex items-center gap-2 text-muted"><span>{t("replyDraft.sent", { name: route?.name ?? "" })}</span>{sendStatus === "waiting" && <Button size="sm" variant="quiet" onClick={() => measureToday("T12", "reach", "undone", () => undoSend(id))}>{t("meetingNote.undo")}</Button>}</div>
            : sendStatus === "unknown" ? <p role="alert" className="text-error">{t("meetingNote.unknown")}</p>
            : <div className="flex gap-2"><Button size="sm" disabled={!draft || status !== "ready" || !body.trim() || !subject.trim()} onClick={() => measureToday("T12", "reach", "done", () => queueSend(ref, id, subject.trim(), body))}>{t("replyDraft.send")}</Button><Button size="sm" variant="secondary" disabled={!draft || status !== "ready"} onClick={() => measureToday("T12", "open", "opened", () => setEditing((value) => !value))}>{t(editing ? "meetingNote.done" : "replyDraft.edit")}</Button></div>}
        {send?.status === "failed" && <p role="alert" className="text-error">{t("meetingNote.sendFailed")}</p>}
    </div>;
}
