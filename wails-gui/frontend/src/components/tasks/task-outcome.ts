import type { DesktopTaskThread } from "@/lib/wails-shim-app";
import type { TFunction } from "i18next";
import { computerClock } from "@/lib/brief-time";

/** The step a running task is on, from its progress: one count for the row and the thread. */
export function currentStep(progress?: { done: number; total: number }): { step: number; total: number } | undefined {
    if (!progress?.total) return undefined;
    return { step: Math.min(progress.done + 1, progress.total), total: progress.total };
}

export function taskOutcomeLine(outcome: DesktopTaskThread["outcome"], language: string, t: TFunction<"app">): string | undefined {
    if (!outcome) return undefined;
    const params = outcome.params ?? {};
    const date = new Date(outcome.at);
    const time = Number.isNaN(date.getTime()) ? "" : computerClock(date, language);
    switch (outcome.kind) {
        case "marked_done": return t("taskThread.markedDone");
        case "completed": return params.summary;
        case "replied": return params.person ? t("taskThread.replied", { person: params.person }) : undefined;
        case "sent": return params.person && params.thing ? t("taskThread.sentOutcome", { person: params.person, thing: params.thing }) : undefined;
        case "you_did": return params.app && time ? t("taskThread.youDid", { app: params.app, time }) : undefined;
        case "already_done": return params.did && time ? t("taskThread.alreadyDone", { did: params.did, time }) : undefined;
        case "hold_placed": return params.when ? t("taskThread.holdPlaced", { when: params.when }) : undefined;
        case "stopped": return time ? params.n && params.m ? t("taskThread.youStoppedSteps", { time, n: params.n, m: params.m }) : t("taskThread.youStopped", { time }) : undefined;
        case "routine_sent": return params.audience && time ? t("taskThread.routineSent", { audience: params.audience, time }) : undefined;
        default: return undefined;
    }
}
