import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { StreamItem } from "@/types";
import { useFirstDay } from "@/hooks/useFirstDay";
import { clockTime } from "@/lib/brief-time";
import { firstDayStarters } from "@/lib/first-day-starters";

/**
 * The first day in Chat (first-day handoff §4): Isoform's second message,
 * "A few things I can do right now…", with up to three starters from today's
 * calendar and the Claude Code or Codex history. A starter is sent as the
 * person's message at once (Chat K4) and leaves the row. It goes once the
 * person has sent something, and with no starter the message doesn't show.
 */
export function FirstDayChatStarters({ items, onPrepare }: { items: StreamItem[] | null; onPrepare?: (text: string) => void }) {
    const { t, i18n } = useTranslation("app");
    const { t: tc } = useTranslation("common");
    const { t: tToday } = useTranslation("today");
    const firstDay = useFirstDay();
    const [picked, setPicked] = useState<string | null>(null);
    if (!firstDay.firstDays || !onPrepare) return null;
    if ((items ?? []).some((item) => item.kind === "user")) return null;
    const clock = (at: number) => clockTime(new Date(at), i18n.language, (key, options) => String(tToday(key as never, options as never)));
    const starters = firstDayStarters(firstDay.calendar, firstDay.history, new Date(), {
        t: (key, options) => String(t(key as never, options as never)),
        fromCalendar: tc("firstDay.fromCalendar"),
        clock,
        language: i18n.language,
    });
    const left = starters.filter((starter) => starter.id !== picked);
    if (left.length === 0) return null;
    return (
        <div className="flex flex-col gap-3" data-testid="first-day-starters">
            <p className="text-[15px] leading-[1.6] text-ink">{t("firstDay.starters")}</p>
            <ul className="flex flex-col overflow-hidden rounded-xl border border-border bg-panel">
                {left.map((starter) => (
                    <li key={starter.id} className="border-b border-border-divider last:border-0">
                        <button
                            type="button"
                            data-starter={starter.id}
                            onClick={() => {
                                setPicked(starter.id);
                                onPrepare(starter.title);
                            }}
                            className="flex w-full flex-col items-start gap-0.5 px-4 py-3 text-left transition-colors hover:bg-surface-hover"
                        >
                            <span className="text-[14px] font-medium text-ink">{starter.title}</span>
                            <span className="text-[12px] text-muted">{starter.source}</span>
                        </button>
                    </li>
                ))}
            </ul>
            <p className="text-[12px] text-muted" data-testid="first-day-local-line">{tc("firstDay.localLine")}</p>
        </div>
    );
}
