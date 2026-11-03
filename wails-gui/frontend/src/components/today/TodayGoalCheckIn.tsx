import { measureToday } from "@/lib/today-telemetry";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { NoteGoalCheckInShown, type GoalCheckInView } from "@/lib/wails-shim-app";
import { GoalMoveNotice, useGoalMoves } from "./TodayGoalMenu";

/** The one quiet check-in on Today for a goal long past due with nothing on
 *  it (Go: goal_checkin.go): "Still working on Discovery?" with Keep, Done
 *  and Forget. It has its own card after Your topics (RC 1) and is never a
 *  notification. Showing it tells the computer, so it is
 *  asked once. */
export function TodayGoalCheckIn({ checkIn, dueLabel, onHidden, onAnswered, active = true }: {
    checkIn: GoalCheckInView;
    active?: boolean;
    /** The due date in the brief's language and zone. */
    dueLabel: string | null;
    onHidden: (goalId: string, hidden: boolean) => void;
    /** The user answered; Today reads the check-in again. */
    onAnswered: () => void;
}) {
    const { t } = useTranslation("today");
    const { move, notice, undo } = useGoalMoves(onHidden, undefined, onAnswered);
    const [busy, setBusy] = useState(false);
    const noted = useRef<string | null>(null);
    useEffect(() => {
        const key = JSON.stringify([checkIn.goalId, checkIn.eventId]);
        if (!active || noted.current === key) return;
        noted.current = key;
        void NoteGoalCheckInShown(checkIn.goalId, checkIn.eventId).catch(() => undefined);
    }, [active, checkIn.goalId, checkIn.eventId]);
    const answer = async (action: "keep" | "resolve" | "forget") => {
        setBusy(true);
        await move(checkIn.goalId, checkIn.title, action, checkIn.eventId);
        setBusy(false);
    };
    return (
        <section
            aria-label={t("todayBrief.goalCheckIn.label")}
            className="flex flex-wrap items-center gap-x-3.5 gap-y-2.5 rounded-xl border border-border bg-panel px-[18px] py-3.5"
            data-testid="goal-checkin"
        >
            <div className="flex min-w-0 flex-[1_1_320px] flex-col gap-0.5">
                <p className="break-words text-[14px] font-semibold text-ink">{t("todayBrief.goalCheckIn.question", { title: checkIn.title })}</p>
                {dueLabel && <p className="text-[12.5px] leading-snug text-muted">{t("todayBrief.goalCheckIn.reason", { date: dueLabel })}</p>}
            </div>
            {notice && <GoalMoveNotice notice={notice} undo={undo} />}
            {!notice?.complete && <div className="flex shrink-0 flex-wrap items-center gap-2">
                <Button size="sm" variant="secondary" disabled={busy} aria-busy={busy} onClick={() => void measureToday("T27", "do", "done", () => answer("keep"))}>
                    {t("todayBrief.goalCheckIn.keep")}
                </Button>
                <Button size="sm" variant="secondary" disabled={busy} onClick={() => void measureToday("T27", "do", "done", () => answer("resolve"))}>
                    {t("todayBrief.goalCheckIn.done")}
                </Button>
                <Button size="sm" variant="quiet" disabled={busy} onClick={() => void measureToday("T27", "do", "done", () => answer("forget"))}>
                    {t("todayBrief.goalCheckIn.forget")}
                </Button>
            </div>}
        </section>
    );
}
