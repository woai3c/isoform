import { measureToday, recordTodayAction } from "@/lib/today-telemetry";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { CHAT_FAILURE_LINK_CLASS } from "@/components/chat-failure-line";
import { ChangeMorningBriefWeekIntent, PostMorningBriefWeekAnswerFromChip, SaveMorningBriefWeekIntent } from "@/lib/wails-shim-app";

/** `chat` is the style of every Isoform question in Chat (C4): the question
 *  in body text under "Isoform", then "You answered: … · Change". */
export function MorningWeekQuestion({ briefID, recommendation, initialAnswer, variant = "today" }: { briefID: string; recommendation?: string; initialAnswer?: string; variant?: "today" | "chat" }) {
    const { t } = useTranslation("today");
    const { t: tChat } = useTranslation("chat");
    const chat = variant === "chat";
    const [answer, setAnswer] = useState(initialAnswer ?? recommendation ?? "");
    const [editing, setEditing] = useState(!initialAnswer && !recommendation);
    const [busy, setBusy] = useState(false);
    const [savedAnswer, setSavedAnswer] = useState(initialAnswer ?? "");
    const [failed, setFailed] = useState(false);
    useEffect(() => {
        if (initialAnswer) { setSavedAnswer(initialAnswer); setAnswer(initialAnswer); }
    }, [initialAnswer]);

    const save = async () => {
        if (!answer.trim() || busy) return;
        const started = performance.now();
        setBusy(true);
        setFailed(false);
        try {
            if (!editing) await PostMorningBriefWeekAnswerFromChip(briefID, savedAnswer, answer.trim());
            else if (savedAnswer) await ChangeMorningBriefWeekIntent(briefID, savedAnswer, answer.trim());
            else await SaveMorningBriefWeekIntent(briefID, answer.trim());
            recordTodayAction("T12", "do", editing ? "done" : "sent", started);
            setSavedAnswer(answer.trim());
            setEditing(false);
        } catch {
            recordTodayAction("T12", "do", "failed", started);
            setFailed(true);
        } finally {
            setBusy(false);
        }
    };

    return (
        <section className={chat ? "flex flex-col gap-1.5" : "flex flex-col gap-2 border-t border-border-divider pt-4"} data-testid="morning-week-question">
            <p className={chat ? "m-0 text-[14px] leading-normal text-ink" : "text-[14px] font-semibold text-ink"}>{t("weekQuestion.question")}</p>
            {savedAnswer && !editing && chat ? <p role="status" className="m-0 text-[12.5px] text-muted" data-week-answered>
                <span>{tChat("mainChat.answered", { answer: savedAnswer })}</span>
                <span aria-hidden="true">{tChat("errors.cause.separator")}</span>
                <button type="button" className={CHAT_FAILURE_LINK_CLASS} onClick={() => measureToday("T12", "open", "opened", () => setEditing(true))}>{tChat("mainChat.change")}</button>
            </p> : savedAnswer && !editing ? <div className="flex flex-wrap items-center gap-2 text-[13px] text-muted">
                <span>{t("weekQuestion.answered", { answer: savedAnswer })}</span>
                <Button size="sm" variant="quiet" onClick={() => measureToday("T12", "open", "opened", () => setEditing(true))}>{t("weekQuestion.change")}</Button>
            </div> : <>
                {editing ? <input autoFocus className="rounded-md border border-border bg-panel px-3 py-2 text-sm text-ink" value={answer}
                    maxLength={160} aria-label={t("weekQuestion.question")} placeholder={t("weekQuestion.placeholder")}
                    onChange={(event) => setAnswer(event.target.value)} /> : <p className="text-[13px] text-muted">{t("weekQuestion.recommendation", { recommendation })}</p>}
                <div className="flex flex-wrap gap-2">
                    <Button size="sm" disabled={busy || !answer.trim()} onClick={() => void save()}>
                        {t(editing ? "weekQuestion.save" : "weekQuestion.stillThat")}
                    </Button>
                    {!editing && <Button size="sm" variant="secondary" disabled={busy} onClick={() => measureToday("T12", "open", "opened", () => setEditing(true))}>{t("weekQuestion.somethingElse")}</Button>}
                </div>
                {failed && <p className="text-[13px] text-muted" role="alert">{t("weekQuestion.failed")}</p>}
            </>}
        </section>
    );
}
