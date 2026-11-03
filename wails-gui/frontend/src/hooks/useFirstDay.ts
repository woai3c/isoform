import { useCallback, useEffect, useState } from "react";
import type { TFunction } from "i18next";
import type { AssistantHistoryView, CalendarFirstDay } from "@/lib/wails-shim-app";
import { GetAssistantHistory, GetCalendarFirstDay, GetFirstWeekDay, usePageQueryRefresh } from "@/lib/page-queries";
import { pageQueryCache } from "@/lib/query-cache";

/** The first two days (first-week day 0 and 1) show what Isoform already
 *  has; on day 3 (index 2) every first-day line goes. */
export const FIRST_DAYS = 2;

export type FirstDay = {
    /** First-week day, 0 on the day setup finished; -1 unknown or before setup; null not read yet. */
    day: number | null;
    /** Day 1 or day 2 of the first week. */
    firstDays: boolean;
    history: AssistantHistoryView | null;
    calendar: CalendarFirstDay | null;
};

function useCachedRead<T>(name: string, read: () => Promise<T>, enabled = true): T | null {
    const [value, setValue] = useState<T | null>(() => pageQueryCache.peek<T>(name) ?? null);
    const load = useCallback(() => {
        Promise.resolve().then(read).then((next) => setValue(next ?? null)).catch(() => {});
    }, [read]);
    useEffect(() => {
        if (enabled) load();
    }, [enabled, load]);
    usePageQueryRefresh([name], load, enabled);
    return value;
}

/** What the first day's pages open on: saved data first, from the page cache,
 *  then the fresh read; later reads follow the history and calendar events. */
export function useFirstDay(): FirstDay {
    const rawDay = useCachedRead<number>("GetFirstWeekDay", GetFirstWeekDay);
    const day = rawDay === null ? null : typeof rawDay === "number" ? rawDay : -1;
    const firstDays = day !== null && day >= 0 && day < FIRST_DAYS;
    const history = useCachedRead<AssistantHistoryView>("GetAssistantHistory", GetAssistantHistory, firstDays);
    const calendar = useCachedRead<CalendarFirstDay>("GetCalendarFirstDay", GetCalendarFirstDay, firstDays);
    return { day, firstDays, history: firstDays ? history : null, calendar: firstDays ? calendar : null };
}

/** History that has something to show: switched on and found. */
export function historyFound(history: AssistantHistoryView | null): history is AssistantHistoryView {
    return !!history && history.enabled && (history.state === "reading" || history.state === "ready");
}

/** "Claude Code history", "Codex history" or "Claude Code and Codex history"
 *  (handoff §8 Names), from the assistants the history read found. */
export function historyName(assistants: readonly string[] | undefined, t: TFunction<"common">): string {
    const claude = assistants?.includes("claude") ?? false;
    const codex = assistants?.includes("codex") ?? false;
    if (claude && codex) return t("firstDay.history.both");
    if (codex) return t("firstDay.history.codex");
    return t("firstDay.history.claude");
}
