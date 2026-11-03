import { useSyncExternalStore } from "react";
import type { TFunction } from "i18next";
import { StartHandoffAgain } from "@/lib/wails-shim-app";
import { onPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";
import { EventsEmit } from "../../../wailsjs/runtime/runtime";

/** The AI a stop cause names, for {AI} in the copy. */
export function stopAssistantName(runner: string | undefined, t: TFunction<"app">): string {
    switch (runner) {
        case "claude_code": return t("taskThread.aiClaudeCode");
        case "codex": return t("taskThread.aiCodex");
        default: return t("taskThread.aiGeneric");
    }
}

/** The Needs you row's line for a task that stopped without finishing. */
export function stopRowLine(cause: string | undefined, runner: string | undefined, t: TFunction<"app">): string | undefined {
    const ai = stopAssistantName(runner, t);
    switch (cause) {
        case "no_assistant": return t("taskThread.stopNoAssistant");
        case "signed_out": return t("taskThread.stopSignedOut", { ai });
        case "restarted": return t("taskThread.stopRestarted");
        case "timeout": return t("taskThread.stopTimeout");
        case "assistant_error": return t("taskThread.stopError", { ai });
        case "loop_guard": return t("taskThread.stopLoopGuard");
        default: return undefined;
    }
}

export type StopButton = "set_up" | "sign_in" | "start_again";
export function stopRowButton(cause: string | undefined): StopButton | undefined {
    switch (cause) {
        case "no_assistant": return "set_up";
        case "signed_out": return "sign_in";
        case "restarted":
        case "timeout":
        case "assistant_error": return "start_again";
        default: return undefined;
    }
}

/** Set up and Sign in open the Your AI screen in Settings, as setup does. */
export function openYourAI() { EventsEmit("settings:open", "ai-provider"); }

/** The thread's own words for a coded stop or Start again message. */
export function stopEventText(code: string | undefined, params: Record<string, string> | undefined, time: string, t: TFunction<"app">): string | undefined {
    const ai = stopAssistantName(params?.assistant, t);
    switch (code) {
        case "started_again": return t("taskThread.startedAgain", { time });
        case "stopped_no_assistant": return t("taskThread.threadNoAssistant", { time });
        case "stopped":
            switch (params?.cause) {
                case "loop_guard": return t("taskThread.threadLoopGuard", { time });
                case "restarted": return t("taskThread.threadStopped", { time, cause: t("taskThread.causeRestarted") });
                case "timeout": return t("taskThread.threadStopped", { time, cause: t("taskThread.causeTimeout") });
                case "signed_out": return t("taskThread.stopSignedOut", { ai });
                default: return t("taskThread.threadStopped", { time, cause: t("taskThread.causeError", { ai }) });
            }
        default: return undefined;
    }
}

// Start again and Start over wait 5 s with "Started · Undo" before running.
interface PendingRestart { timer: number }
let pending: Record<string, PendingRestart> = {};
const listeners = new Set<() => void>();
function publish(next: Record<string, PendingRestart>) { pending = next; listeners.forEach((listener) => listener()); }
function subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; }
export function usePendingRestart(id: string): PendingRestart | undefined {
    return useSyncExternalStore(subscribe, () => pending[id], () => pending[id]);
}
/** Do it, Start again and Start over all wait 5 s with "Started · Undo"; run defaults to Start again. */
export function beginRestart(id: string, failed: () => void, done?: () => void, run: () => Promise<unknown> = () => StartHandoffAgain(id)) {
    if (pending[id]) return;
    const item: PendingRestart = { timer: window.setTimeout(() => {
        item.timer = 0;
        publish({ ...pending });
        const { [id]: _removed, ...rest } = pending;
        void Promise.resolve().then(run).then(() => { publish(rest); done?.(); }).catch(() => { publish(rest); failed(); });
    }, 5000) };
    publish({ ...pending, [id]: item });
}
export function undoRestart(id: string) {
    const item = pending[id];
    if (!item?.timer) return;
    window.clearTimeout(item.timer);
    const { [id]: _removed, ...rest } = pending;
    publish(rest);
}
onPagePrivacyWithdrawal(() => {
    Object.values(pending).forEach((item) => window.clearTimeout(item.timer));
    publish({});
});
export function usePendingRestarts(): Record<string, PendingRestart> {
    return useSyncExternalStore(subscribe, () => pending, () => pending);
}
