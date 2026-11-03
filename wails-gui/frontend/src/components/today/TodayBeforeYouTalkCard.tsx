import { measureToday } from "@/lib/today-telemetry";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { MoreHorizontal } from "lucide-react";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { RecordTodayAction, type TodayBeforeYouTalk } from "@/lib/wails-shim-app";
import type { TodayActionHandlers } from "./types";
import { useTodaySectionSeen } from "@/hooks/useTodaySectionSeen";
import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";
import { computerClock } from "@/lib/brief-time";

export function TodayBeforeYouTalkCard({ card, handlers, onDismiss, onMute, active = true }: {
    card: TodayBeforeYouTalk;
    handlers: TodayActionHandlers;
    onDismiss: (meetingID: string) => Promise<void>;
    onMute?: () => Promise<void>;
    active?: boolean;
}) {
    const seenRef = useTodaySectionSeen(active, "before_you_talk", 1);
    const { t, i18n } = useTranslation("today");
    const tr = (key: string, options?: Record<string, unknown>) => t(key as never, options as never) as unknown as string;
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    const [clock, setClock] = useState(() => Date.now());
    useEffect(() => {
        if (!active) return;
        const current = Date.now();
        setClock(current);
        const timers = [card.startAt - 10 * 60_000, card.endAt]
            .filter((at) => at > current)
            .map((at) => setTimeout(() => setClock(Date.now()), at - current));
        return () => timers.forEach(clearTimeout);
    }, [active, card.startAt, card.endAt]);
    const now = new Date(clock);
    const start = new Date(card.startAt);
    const time = computerClock(start, i18n.language);
    const minutes = Math.max(0, Math.ceil((card.startAt - now.getTime()) / 60_000));
    const relative = minutes === 0 ? t("beforeYouTalk.now") : t("beforeYouTalk.inMinutes", { count: minutes });
    const firstName = card.personName.trim().split(/\s+/)[0];
    const context = { sourceKind: "calendar", sourceId: card.meetingId };
    let conferenceUrl: string | null = null;
    try {
        const url = new URL(card.conferenceUrl ?? "");
        if (url.protocol === "https:" || url.protocol === "http:") conferenceUrl = url.toString();
    } catch {
        // A missing or malformed conference link cannot be opened.
    }
    const canJoin = !!conferenceUrl && clock >= card.startAt - 10 * 60_000 && clock < card.endAt;
    const dismiss = async () => {
        const started = performance.now();
        setBusy(true);
        setFailed(false);
        let outcome: "done" | "failed" = "done";
        try {
            await onDismiss(card.meetingId);
        } catch {
            setFailed(true);
            outcome = "failed";
        } finally {
            setBusy(false);
        }
        void RecordTodayAction("today", "T44", "do", outcome, Math.max(0, Math.round(performance.now() - started))).catch(() => undefined);
    };
    const join = () => {
        if (!conferenceUrl) return;
        const started = performance.now();
        let outcome: "opened" | "failed" = "opened";
        try {
            BrowserOpenURL(conferenceUrl);
        } catch {
            outcome = "failed";
        }
        void RecordTodayAction("today", "T20", "open", outcome, Math.max(0, Math.round(performance.now() - started))).catch(() => undefined);
    };
    const lastTime = card.lastTime ? lastTimeText(card, i18n.language, tr) : "";
    const theyAsked = card.hasUnsentDraft
        ? card.theyAsked
            ? t("beforeYouTalk.withDraft", { question: card.theyAsked })
            : t("beforeYouTalk.draftOnly")
        : card.theyAsked;

    return (
        <section ref={seenRef} aria-label={t("beforeYouTalk.label")} className="flex flex-col gap-3 rounded-xl border border-border bg-panel px-[18px] py-4" data-testid="before-you-talk">
            <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1.5">
                <span className="text-[11px] font-medium uppercase tracking-wider text-muted">{t("beforeYouTalk.label")}</span>
                <span className="text-[12.5px] text-muted">{t("beforeYouTalk.timing", { title: card.meetingTitle, time, relative })}</span>
                <Button variant="link" className="ml-auto h-auto p-0 text-[12.5px] font-normal" onClick={() => measureToday("T42", "open", "opened", () => handlers.onOpenPeople(card.personId))}>
                    {t("beforeYouTalk.openPerson", { name: card.personName })}
                </Button>
            {onMute && <DropdownMenu>
                <DropdownMenuTrigger asChild><button type="button" aria-label={t("beforeYouTalk.more")} onClick={() => measureToday("T44", "open", "opened", () => undefined)} className="rounded-md p-1 text-muted hover:bg-surface-hover"><MoreHorizontal className="size-4" /></button></DropdownMenuTrigger>
                <DropdownMenuContent align="end"><DropdownMenuItem disabled={busy} onClick={() => {
                    const started = performance.now();
                    setBusy(true); setFailed(false);
                    void onMute().then(() => RecordTodayAction("today", "T44", "do", "done", Math.max(0, Math.round(performance.now() - started))).catch(() => undefined)).catch(() => {
                        setFailed(true);
                        void RecordTodayAction("today", "T44", "do", "failed", Math.max(0, Math.round(performance.now() - started))).catch(() => undefined);
                    }).finally(() => setBusy(false));
                }}>{t("beforeYouTalk.mute")}</DropdownMenuItem></DropdownMenuContent>
            </DropdownMenu>}
            </div>
            <h2 className="text-[16px] font-semibold text-ink">{card.personName}</h2>
            <div className="flex flex-col gap-2">
                <BeforeYouTalkLine label={t("beforeYouTalk.youOwe")} text={card.youOwe} />
                <BeforeYouTalkLine label={t("beforeYouTalk.theyAsked")} text={theyAsked} />
                <BeforeYouTalkLine label={t("beforeYouTalk.lastTime")} text={lastTime} />
            </div>
            <div className="flex flex-wrap items-center gap-2">
                {canJoin && <Button size="sm" onClick={join}>{t("todayBrief.join")}</Button>}
                <Button size="sm" variant={canJoin ? "secondary" : "default"} onClick={() => {
                    measureToday("T43", "ask", "opened", () => {
                        const prompt = t("beforeYouTalk.briefPrompt", { title: card.meetingTitle, name: card.personName, time });
                        if (handlers.onBriefMeeting) handlers.onBriefMeeting(card, prompt);
                        else handlers.onStartChat(prompt, context);
                    });
                }}>
                    {t("beforeYouTalk.briefMe")}
                </Button>
                <Button size="sm" variant="secondary" onClick={() => {
                    measureToday("T43", "do", "opened", () => {
                        const prompt = t("beforeYouTalk.draftPrompt", { title: card.meetingTitle, name: card.personName });
                        if (handlers.onDraftMeetingNote) handlers.onDraftMeetingNote(card, prompt);
                        else handlers.onStartChat(prompt, context);
                    });
                }}>
                    {t("beforeYouTalk.draftNote", { firstName })}
                </Button>
                <Button size="sm" variant="quiet" disabled={busy} aria-busy={busy} onClick={() => void dismiss()}>
                    {t("beforeYouTalk.notNow")}
                </Button>
            </div>
            {failed && <p role="alert" className="text-[12px] text-error">{t("beforeYouTalk.dismissFailed")}</p>}
        </section>
    );
}

function BeforeYouTalkLine({ label, text }: { label: string; text?: string }) {
    if (!text?.trim()) return null;
    return (
        <div className="flex gap-2.5 text-[13.5px] leading-[1.45] text-ink">
            <span className="w-[92px] shrink-0 text-muted">{label}</span>
            <span className="min-w-0">{text}</span>
        </div>
    );
}

function lastTimeText(card: TodayBeforeYouTalk, language: string, t: (key: string, options?: Record<string, unknown>) => string): string {
    if (!card.lastTimeAt) return card.lastTime ?? "";
    const date = new Date(card.lastTimeAt).toLocaleDateString(language, { month: "short", day: "numeric" });
    const minutes = Math.max(1, Math.round((card.lastDurationMs ?? 0) / 60_000));
    return t("beforeYouTalk.lastTimeDetail", {
        date,
        duration: t("beforeYouTalk.duration", { count: minutes }),
        title: card.lastTime,
    });
}
