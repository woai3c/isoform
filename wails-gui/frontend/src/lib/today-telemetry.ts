import { RecordTodayAction } from "@/lib/wails-shim-app";

export type TodayActionKind = "ask" | "do" | "reach" | "open";
export type TodayActionOutcome = "done" | "undone" | "stopped" | "failed" | "sent" | "opened";

/** Only fixed map IDs and outcomes cross the telemetry boundary. */
export function recordTodayAction(action: string, kind: TodayActionKind, outcome: TodayActionOutcome, started: number) {
    try {
        void Promise.resolve(RecordTodayAction("today", action, kind, outcome, Math.max(0, Math.round(performance.now() - started)))).catch(() => undefined);
    } catch {
        // Measurement must not interrupt the person's action.
    }
}

export function measureToday<T>(action: string, kind: TodayActionKind, outcome: TodayActionOutcome, run: () => T): T {
    const started = performance.now();
    try {
        const result = run();
        if (result instanceof Promise) {
            return result.then((value) => { recordTodayAction(action, kind, value === false || value === "failed" ? "failed" : outcome, started); return value; }, (error) => {
                recordTodayAction(action, kind, "failed", started);
                throw error;
            }) as T;
        }
        recordTodayAction(action, kind, result === false || result === "failed" ? "failed" : outcome, started);
        return result;
    } catch (error) {
        recordTodayAction(action, kind, "failed", started);
        throw error;
    }
}
