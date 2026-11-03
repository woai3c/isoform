import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { MoreHorizontal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { MarkdownContent } from "@/components/markdown-content";
import { briefClockTime } from "@/lib/brief-time";
import { CancelOpenAnswer, ChangeOpenAnswer, NotForMeAnswer, SelectOpenAnswerAsk, UndoNotForMeAnswer } from "@/lib/wails-shim-app";
import { getOpenAnswersGeneration, refreshOpenAnswers, updateOpenAnswer, useOpenAnswers } from "@/hooks/useOpenAnswers";
import { parseOpenAnswer, type OpenAnswer, type OpenAnswerDelivery } from "@/lib/open-answers";
import type { StreamItem } from "@/types";

type SendOpenAnswer = (sessionId: string, text: string) => Promise<unknown>;

function clock(value: string | undefined, language: string, t: (key: string, options?: Record<string, unknown>) => string) {
    return briefClockTime(value, language, t as never) ?? "";
}

function useAnswerCopy() {
    const { t, i18n } = useTranslation("chat");
    const language = i18n.resolvedLanguage ?? i18n.language;
    return {
        t,
        language,
        time: (value: string | undefined) => clock(value, language, (key, options) => String(i18n.t(key as never, { ...options, ns: "today", lng: language }))),
    };
}

function promiseText(answer: OpenAnswer, time: (value?: string) => string, t: (key: string, options?: Record<string, unknown>) => string) {
    if (answer.state === "saw_it") return t("mainChat.openAnswers.sawIt", { time: time(answer.sawAt) });
    if (answer.state === "gave_up") {
        return answer.watchedEvent?.who
            ? t("mainChat.openAnswers.gaveUpWithWho", { who: answer.watchedEvent.who })
            : t("mainChat.openAnswers.gaveUp");
    }
    if (answer.state !== "open") return null;
    if (answer.delivery === "brief") return t("mainChat.openAnswers.brief");
    if (answer.delivery === "settled_only") return t("mainChat.openAnswers.settled");
    if (answer.settles === "reply" && answer.watchedEvent?.who) {
        return t("mainChat.openAnswers.reply", { who: answer.watchedEvent.who });
    }
    if (answer.settles === "status") {
        return t("mainChat.openAnswers.aroundStatus", { time: time(answer.windowStart) });
    }
    return t("mainChat.openAnswers.aroundResult", { time: time(answer.windowStart) });
}

export function OpenAnswerPromiseLine({
    answer: initialAnswer,
    onSend,
}: {
    answer: OpenAnswer;
    onSend?: SendOpenAnswer;
}) {
    const { t, time } = useAnswerCopy();
    const { answers, generation, delivered } = useOpenAnswers();
    const current = answers.find((answer) => answer.id === initialAnswer.id) ??
        (delivered.get(initialAnswer.id) === generation ? initialAnswer : undefined);
    const [busy, setBusy] = useState(false);
    const busyRef = useRef(false);
    const [error, setError] = useState(false);
    const [menuOpen, setMenuOpen] = useState(false);
    if (!current) return null;
    const text = promiseText(current, time, t as never);
    const choose = async (delivery: OpenAnswerDelivery) => {
        if (busyRef.current) return;
        const requestGeneration = getOpenAnswersGeneration();
        busyRef.current = true;
        setBusy(true);
        setError(false);
        try {
            const returned = await ChangeOpenAnswer(current.id, delivery);
            const next = returned ? parseOpenAnswer(returned) : undefined;
            if (next) updateOpenAnswer(next, requestGeneration);
            else await refreshOpenAnswers(requestGeneration);
            setMenuOpen(false);
        } catch {
            setError(true);
        } finally {
            busyRef.current = false;
            setBusy(false);
        }
    };
    const cancel = async () => {
        if (busyRef.current) return;
        const requestGeneration = getOpenAnswersGeneration();
        busyRef.current = true;
        setBusy(true);
        setError(false);
        try {
            const returned = await CancelOpenAnswer(current.id);
            const next = returned ? parseOpenAnswer(returned) : null;
            if (next) updateOpenAnswer(next, requestGeneration);
            else await refreshOpenAnswers(requestGeneration);
        } catch {
            setError(true);
        } finally {
            busyRef.current = false;
            setBusy(false);
        }
    };
    const ask = async () => {
        if (busyRef.current) return;
        busyRef.current = true;
        setBusy(true);
        setError(false);
        try {
            if (!onSend) throw new Error();
            await SelectOpenAnswerAsk(current.id);
            await onSend(current.sessionId, t("mainChat.openAnswers.askSend"));
        } catch {
            setError(true);
        } finally {
            busyRef.current = false;
            setBusy(false);
        }
    };
    if (current.state === "cancelled" || current.state === "settled") return null;
    if (current.state === "ask") {
        return (
            <div className="mt-2 flex flex-wrap items-center gap-2 text-[13px] text-muted" data-testid="open-answer-ask">
                <span>{t("mainChat.openAnswers.ask")}</span>
                <Button size="sm" variant="quiet" disabled={busy} onClick={() => void ask()}>{t("mainChat.openAnswers.letMeKnow")}</Button>
                {error && <span role="alert" className="text-subtle">{t("failure.save")}</span>}
            </div>
        );
    }
    if (!text) return null;
    return (
        <div className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-[12.5px] text-muted" data-testid="open-answer-promise">
            <span>{text}</span>
            {current.state === "open" && (
                <>
                    <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
                        <DropdownMenuTrigger asChild><Button size="sm" variant="quiet" disabled={busy}>{t("mainChat.openAnswers.change")}</Button></DropdownMenuTrigger>
                        <DropdownMenuContent align="start">
                            {current.windowStart && <DropdownMenuItem onClick={() => void choose("around")}>{t("mainChat.openAnswers.aroundChoice", { time: time(current.windowStart) })}</DropdownMenuItem>}
                            <DropdownMenuItem onClick={() => void choose("settled_only")}>{t("mainChat.openAnswers.settledChoice")}</DropdownMenuItem>
                            <DropdownMenuItem onClick={() => void choose("brief")}>{t("mainChat.openAnswers.briefChoice")}</DropdownMenuItem>
                        </DropdownMenuContent>
                    </DropdownMenu>
                    <Button size="sm" variant="quiet" disabled={busy} onClick={() => void cancel()}>{t("mainChat.openAnswers.noNeed")}</Button>
                </>
            )}
            {error && <span role="alert" className="text-subtle">{t("failure.save")}</span>}
        </div>
    );
}

export function AnswerReadyCard({ item }: { item: StreamItem }) {
    const { t, i18n } = useTranslation("chat");
    const answer = item.openAnswer;
    const [learned, setLearned] = useState(answer?.learned ?? false);
    useEffect(() => setLearned(answer?.learned ?? false), [answer?.learned]);
    const [busy, setBusy] = useState(false);
    const busyRef = useRef(false);
    const [error, setError] = useState(false);
    if (!answer) return null;
    const created = clock(answer.createdAt, i18n.resolvedLanguage ?? i18n.language, (key, options) => String(i18n.t(key as never, { ...options, ns: "today", lng: i18n.resolvedLanguage ?? i18n.language })));
    const language = i18n.resolvedLanguage ?? i18n.language;
    const timestamp = typeof item.createdAt === "number" && Number.isFinite(item.createdAt) ? new Date(item.createdAt).toISOString() : undefined;
    const itemTime = clock(timestamp, language, (key, options) => String(i18n.t(key as never, { ...options, ns: "today", lng: language })));
    const quote = answer.question.length > 120 ? `${answer.question.slice(0, 117)}…` : answer.question;
    const notForMe = async () => {
        if (busyRef.current) return;
        busyRef.current = true;
        setBusy(true);
        setError(false);
        try {
            await NotForMeAnswer(item.id);
            setLearned(true);
        } catch {
            setError(true);
        } finally {
            busyRef.current = false;
            setBusy(false);
        }
    };
    const undo = async () => {
        if (busyRef.current) return;
        busyRef.current = true;
        setBusy(true);
        setError(false);
        try {
            await UndoNotForMeAnswer(answer.kind);
            setLearned(false);
        } catch {
            setError(true);
        } finally {
            busyRef.current = false;
            setBusy(false);
        }
    };
    return (
        <article className="flex flex-col gap-2 rounded-xl border border-border bg-panel-warm px-3.5 py-3" data-testid="answer-ready-card">
            <div className="flex items-center justify-between gap-2 text-[11px] font-medium text-muted">
                <span>{t("mainChat.openAnswers.youAsked", { time: created })}</span>
                <DropdownMenu>
                    <DropdownMenuTrigger asChild><Button size="icon" variant="quiet" aria-label={t("mainChat.proactive.moreActions")}><MoreHorizontal className="size-4" /></Button></DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                        {!learned && <DropdownMenuItem onClick={() => void notForMe()}>{t("mainChat.openAnswers.notForMe")}</DropdownMenuItem>}
                        {learned && <DropdownMenuItem onClick={() => void undo()}>{t("mainChat.openAnswers.undo")}</DropdownMenuItem>}
                    </DropdownMenuContent>
                </DropdownMenu>
            </div>
            <p className="m-0 truncate text-[12px] italic text-muted" title={answer.question}>“{quote}”</p>
            <div className="select-text text-[15px] leading-relaxed text-ink"><MarkdownContent content={item.content ?? ""} /></div>
            {answer.source && <p className="m-0 text-[12px] text-muted">{t("mainChat.openAnswers.fromSource", { source: answer.source, time: itemTime })}</p>}
            {learned && <div className="flex items-center gap-2 text-[12px] text-muted"><span>{answer.topic ? t("mainChat.openAnswers.learnedTopic", { topic: answer.topic }) : t("mainChat.openAnswers.learned")}</span><Button size="sm" variant="quiet" disabled={busy} onClick={() => void undo()}>{t("mainChat.openAnswers.undo")}</Button></div>}
            {error && <p role="alert" className="m-0 text-[12px] text-subtle">{t("failure.save")}</p>}
        </article>
    );
}
