import type { HandoffEntry } from "@/lib/wails-shim-app";
import { useTodayNeeds } from "@/hooks/useTodayNeeds";

const NEEDS_YOU_STATUSES = new Set(["active", "needs_review", "blocked"]);

/** Kept for callers that classify handoffs outside the shared waiting list. */
export function needsYouFrom(handoffs: readonly HandoffEntry[]): HandoffEntry[] {
    return handoffs.filter((h) => NEEDS_YOU_STATUSES.has(h.status));
}

/** The Tasks badge and Today preview use the same ordered projection. */
export function useTasksNeedsYouCount(): number {
    return useTodayNeeds().items.length;
}
