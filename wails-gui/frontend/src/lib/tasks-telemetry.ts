import { useEffect, useRef } from "react";
import { RecordTasksAction, RecordTasksSectionSeen } from "@/lib/wails-shim-app";
import type { TodayActionKind, TodayActionOutcome } from "@/lib/today-telemetry";

/** A Tasks product-map row id (docs/product-map/desktop-tasks-activity.md). */
export type TasksActionId = `${"T" | "H" | "D" | "C" | "E"}${number}${number}`;
export type TasksSection = "needs_you" | "up_next" | "running" | "scheduled" | "done_this_week" | "all_done";

/** Only fixed map ids and outcomes cross the telemetry boundary; the Mac
 *  drops everything when diagnostic data is off. */
export function recordTasksAction(action: TasksActionId, kind: TodayActionKind, outcome: TodayActionOutcome, started = performance.now()) {
    try {
        void Promise.resolve(RecordTasksAction(action, kind, outcome, Math.max(0, Math.round(performance.now() - started)))).catch(() => undefined);
    } catch {
        // Measurement must not interrupt the person's action.
    }
}

/** Runs an action and records it with `outcome`, or "failed" when it throws,
 *  rejects or resolves false. */
export function measureTasks<T>(action: TasksActionId, kind: TodayActionKind, outcome: TodayActionOutcome, run: () => T): T {
    const started = performance.now();
    try {
        const result = run();
        if (result instanceof Promise) {
            return result.then((value) => { recordTasksAction(action, kind, value === false ? "failed" : outcome, started); return value; }, (error) => {
                recordTasksAction(action, kind, "failed", started);
                throw error;
            }) as T;
        }
        recordTasksAction(action, kind, result === false ? "failed" : outcome, started);
        return result;
    } catch (error) {
        recordTasksAction(action, kind, "failed", started);
        throw error;
    }
}

/** Records a shown section once per visit to Tasks. */
export function useTasksSectionSeen(section: TasksSection, rows: number, shown: boolean) {
    const seen = useRef(false);
    useEffect(() => {
        if (!shown || seen.current) return;
        seen.current = true;
        try { void Promise.resolve(RecordTasksSectionSeen(section, rows)).catch(() => undefined); } catch { /* Measurement cannot block Tasks. */ }
    }, [section, rows, shown]);
}
