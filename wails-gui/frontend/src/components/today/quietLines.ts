import type { QuietLine } from "@/lib/wails-shim-app";
import type { TodayQuietLine } from "./TodayStatus";
import { computerClock } from "@/lib/brief-time";

type Translate = (key: string, options?: Record<string, unknown>) => string;

function clock(iso: string | undefined, language: string): string | null {
    if (!iso) return null;
    const at = new Date(iso);
    return Number.isNaN(at.getTime()) ? null : computerClock(at, language);
}

/**
 * The words of Today's quiet lines (RC 1): one muted sentence per notice kind
 * the user moved from notifications to Today. Observing paused names its
 * stretches ("Observing paused 8:40 AM to 8:55 AM"); other kinds say when,
 * or how often and when last. A kind without a known name is left out, never
 * shown as a code.
 */
export function describeQuietLines(
    lines: readonly QuietLine[],
    tr: Translate,
    kindLabel: (kind: string) => string | null,
    language: string,
): TodayQuietLine[] {
    const out: TodayQuietLine[] = [];
    for (const line of lines) {
        const label = kindLabel(line.kind);
        if (!label) continue;
        const stretches = (line.intervals ?? []).map((interval) => {
            const start = clock(interval.start, language);
            const end = clock(interval.end, language);
            if (!start) return null;
            return end ? tr("quiet.interval", { start, end }) : tr("quiet.since", { start });
        }).filter((text): text is string => !!text);
        let text: string | null = null;
        if (stretches.length > 0) {
            // The language's own list separator: ", " in English, "、" in Chinese.
            text = tr("quiet.withIntervals", { label, intervals: stretches.join(tr("quiet.separator")) });
        } else if (line.count > 1 && clock(line.lastAt, language)) {
            text = tr("quiet.many", { label, count: line.count, time: clock(line.lastAt, language) });
        } else if (clock(line.lastAt, language)) {
            text = tr("quiet.once", { label, time: clock(line.lastAt, language) });
        }
        if (text) out.push({ id: line.kind, text });
    }
    return out;
}
