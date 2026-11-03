import { measureToday } from "@/lib/today-telemetry";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { CheckCircle2, EyeOff, MoreHorizontal } from "lucide-react";
import { MoveGoal, type GoalMoveAction } from "@/lib/wails-shim-app";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

/** How long Done and Forget can be undone. */
export const GOAL_UNDO_MS = 10_000;

// A fresh key per click; the computer ignores a repeated key.
function newMoveKey(): string {
    if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
        return crypto.randomUUID();
    }
    return `${Date.now().toString(16)}-${Math.random().toString(16).slice(2, 10)}`;
}

/** The i18n key for a refused move. MoveGoal names its errors. */
function moveErrorKey(error: unknown): "todayBrief.goalMenu.changed" | "todayBrief.goalMenu.unavailable" {
    return String(error).includes("goal_changed") ? "todayBrief.goalMenu.changed" : "todayBrief.goalMenu.unavailable";
}

/** Moves a goal from Today. Done and Forget hide its row at once
 *  (onHidden(true)) and offer Undo for 10 seconds; a refused move or Undo
 *  brings the row back. Keep (the check-in's) changes nothing on the page. */
export function useGoalMoves(onHidden: (goalId: string, hidden: boolean) => void, onRefused?: () => void, onSettled?: () => void) {
    const [notice, setNotice] = useState<{ text: string; goalId?: string; eventId?: string; complete?: boolean } | null>(null);
    const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
    const { t } = useTranslation("today");
    useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);
    const undo = async () => {
        if (!notice?.goalId || !notice.eventId) return;
        try {
            await MoveGoal({ goalId: notice.goalId, expectedEventId: notice.eventId, action: "restore", key: newMoveKey() });
            if (timer.current) clearTimeout(timer.current);
            timer.current = null;
            onHidden(notice.goalId, false);
            setNotice(null);
            onSettled?.();
        } catch {
            setNotice({ ...notice, text: t("todayBrief.goalMenu.undoFailed") });
        }
    };
    const move = async (goalId: string, title: string, action: Extract<GoalMoveAction, "resolve" | "forget" | "keep">, expectedEventId?: string): Promise<boolean> => {
        try {
            const result = await MoveGoal({ goalId, expectedEventId, action, key: newMoveKey() });
            if (timer.current) clearTimeout(timer.current);
            setNotice(action === "keep"
                ? { text: t("todayBrief.goalCheckIn.keptToast", { title }), complete: true }
                : { text: t(action === "resolve" ? "todayBrief.goalMenu.markedDone" : "todayBrief.goalMenu.forgotToast", { title }), goalId, eventId: result.eventId, complete: true });
            timer.current = setTimeout(() => {
                if (action !== "keep") onHidden(goalId, true);
                setNotice(null);
                onSettled?.();
            }, GOAL_UNDO_MS);
            return true;
        } catch (error) {
            setNotice({ text: t(moveErrorKey(error)) });
            onRefused?.();
            return false;
        }
    };
    return { move, notice, undo };
}

export function GoalMoveNotice({ notice, undo }: { notice: { text: string; goalId?: string; eventId?: string; complete?: boolean } | null; undo: () => Promise<void> }) {
    const { t } = useTranslation("today");
    if (!notice) return null;
    return <p role="status" className="text-[12px] text-muted"><span>{notice.text}</span>{notice.eventId && <> · <button type="button" className="text-accent underline" onClick={() => void measureToday("T26", "do", "undone", undo)}>{t("todayBrief.goalMenu.undo")}</button></>}</p>;
}

/** The "..." on a goal row on Today: Mark done and Forget this goal, each
 *  with Undo. Shown on row hover and keyboard focus; the row is the `group`. */
export function TodayGoalMenu({ goalId, title, onHidden }: {
    goalId: string;
    title: string;
    onHidden: (goalId: string, hidden: boolean) => void;
}) {
    const { t } = useTranslation("today");
    const { move, notice, undo } = useGoalMoves(onHidden);
    return (
        <div className="flex flex-col items-end gap-1"><DropdownMenu>
            <DropdownMenuTrigger asChild>
                <button
                    type="button"
                    onClick={() => measureToday("T24", "open", "opened", () => undefined)}
                    data-testid="goal-menu"
                    aria-label={t("todayBrief.goalMenu.more", { title })}
                    className={cn(
                        "flex size-6 shrink-0 items-center justify-center rounded-md text-muted transition-[opacity,color,background-color]",
                        "opacity-0 hover:bg-ink/10 hover:text-ink focus-visible:opacity-100 group-hover:opacity-100 group-focus-within:opacity-100 data-[state=open]:opacity-100",
                    )}
                >
                    <MoreHorizontal className="size-3.5" />
                </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" sideOffset={4}>
                <DropdownMenuItem onClick={() => void measureToday("T24", "do", "done", () => move(goalId, title, "resolve"))}>
                    <CheckCircle2 className="size-3.5" />
                    {t("todayBrief.goalMenu.markDone")}
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => void measureToday("T25", "do", "done", () => move(goalId, title, "forget"))}>
                    <EyeOff className="size-3.5" />
                    {t("todayBrief.goalMenu.forget")}
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu><GoalMoveNotice notice={notice} undo={undo} /></div>
    );
}
