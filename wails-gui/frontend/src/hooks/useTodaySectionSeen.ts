import { useEffect, useRef, useState } from "react";
import { RecordTodaySectionSeen } from "@/lib/wails-shim-app";

export type TodaySection = "brief" | "before_you_talk" | "needs_you" | "done_for_you" | "your_topics" | "your_week" | "getting_to_know_you" | "your_projects";

/** Record a mounted section once for each visit to Today. */
export function useTodaySectionSeen(active: boolean, section: TodaySection, rows: number, ready = true) {
    const [element, setElement] = useState<HTMLElement | null>(null);
    const seen = useRef(false);
    useEffect(() => {
        if (!active) { seen.current = false; return; }
        if (!ready || !element || seen.current) return;
        seen.current = true;
        try { void Promise.resolve(RecordTodaySectionSeen(section, rows)).catch(() => undefined); } catch { /* Measurement cannot block Today. */ }
    }, [active, element, rows, section, ready]);
    return setElement;
}
