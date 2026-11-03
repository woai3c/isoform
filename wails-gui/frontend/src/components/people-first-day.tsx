import { useTranslation } from "react-i18next";
import type { CalendarWeekPerson } from "@/lib/wails-shim-app";
import { clockTime } from "@/lib/brief-time";

/** "1:1 today at 2:30 PM · recurring", or the day when it isn't today. */
export function nextMeetingLine(person: CalendarWeekPerson, now: Date, language: string, t: (key: string, options?: Record<string, unknown>) => string, clock: (at: Date) => string): string {
    if (!person.nextMeetingAt) return "";
    const at = new Date(person.nextMeetingAt);
    const line = at.toDateString() === now.toDateString()
        ? t("firstDay.nextToday", { title: person.nextMeetingTitle, time: clock(at) })
        : t("firstDay.nextOn", { title: person.nextMeetingTitle, day: at.toLocaleDateString(language, { weekday: "long" }), time: clock(at) });
    return person.recurring ? `${line} · ${t("firstDay.recurring")}` : line;
}

/**
 * Who you know on the first days (first-day handoff §5): the people from
 * this week's calendar, each with the next meeting. A row opens the person
 * when Isoform already knows them.
 */
export function CalendarPeopleSection({ people, known, onSelect, now = new Date() }: {
    people: CalendarWeekPerson[];
    known: (principalId: string) => boolean;
    onSelect: (principalId: string) => void;
    now?: Date;
}) {
    const { t, i18n } = useTranslation("people");
    const { t: tToday } = useTranslation("today");
    const tr = (key: string, options?: Record<string, unknown>) => String(t(key as never, options as never));
    const clock = (at: Date) => clockTime(at, i18n.language, (key, options) => String(tToday(key as never, options as never)));
    if (people.length === 0) return null;
    return (
        <section aria-labelledby="people-calendar-h" className="flex flex-col gap-1" data-testid="people-calendar">
            <h2 id="people-calendar-h" className="mb-2 text-[18px] font-semibold text-ink">{tr("firstDay.calendarGroup")}</h2>
            {people.map((person) => {
                const name = person.name || person.email;
                const line = nextMeetingLine(person, now, i18n.language, tr, clock);
                const body = (
                    <>
                        <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-surface-active text-[13px] font-medium text-muted" aria-hidden>
                            {name.trim().charAt(0).toUpperCase()}
                        </span>
                        <span className="min-w-0 flex-1">
                            <span className="block truncate text-[14px] text-ink">{name}</span>
                            {line && <span className="block truncate text-[12px] text-muted">{line}</span>}
                        </span>
                    </>
                );
                const openable = !!person.principalId && known(person.principalId);
                return openable ? (
                    <button key={person.key} type="button" data-calendar-person={person.key} onClick={() => onSelect(person.principalId)}
                        className="flex items-center gap-3 rounded-lg px-2 py-2 text-left transition-colors hover:bg-surface-hover">
                        {body}
                    </button>
                ) : (
                    <div key={person.key} data-calendar-person={person.key} className="flex items-center gap-3 px-2 py-2">{body}</div>
                );
            })}
        </section>
    );
}
