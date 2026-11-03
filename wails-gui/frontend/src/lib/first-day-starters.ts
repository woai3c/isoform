import type { AssistantHistoryView, CalendarFirstDay, HistoryProject } from "@/lib/wails-shim-app";

type Translate = (key: string, options?: Record<string, unknown>) => string;

/** One first-day starter (first-day handoff §4): words for the box and where
 *  they came from. A starter prepares; only Send sends. */
export type FirstDayStarter = { id: "meeting" | "problem" | "plan"; title: string; source: string };

export type StarterText = {
    /** The app namespace's translate. */
    t: Translate;
    /** "From your calendar" (common.firstDay.fromCalendar). */
    fromCalendar: string;
    /** A clock time in Today's one clock. */
    clock: (at: number) => string;
    language: string;
};

const HOUR_MS = 3_600_000;

function firstName(name: string): string {
    return name.trim().split(/\s+/)[0] ?? "";
}

/** The project with an open problem, most recent sessions first. */
export function projectWithProblem(history: AssistantHistoryView | null): HistoryProject | null {
    if (!history || history.state !== "ready") return null;
    return [...history.projects].sort((a, b) => b.sessions14d - a.sessions14d || b.lastAt - a.lastAt)
        .find((project) => project.openProblem && project.openQuestion) ?? null;
}

function assistantName(project: HistoryProject): string {
    return project.assistants.length === 1 && project.assistants[0] === "codex" ? "Codex" : "Claude Code";
}

/** "Prep me for 2:30 with Maya", or with the meeting's title when nobody else
 *  is named. Null without a meeting in the next three hours. */
export function meetingStarter(calendar: CalendarFirstDay | null, text: StarterText): string | null {
    const meeting = calendar?.connected ? calendar.nextMeeting : null;
    if (!meeting) return null;
    const time = text.clock(meeting.start);
    const name = firstName(meeting.attendeeNames?.[0] ?? "");
    return name ? text.t("firstDay.prepMeeting", { time, name }) : text.t("firstDay.prepMeetingTitle", { time, title: meeting.title });
}

/** Up to three starters: the next meeting, a project's open problem, and an
 *  hour or more free today. Fewer sources, fewer starters. */
export function firstDayStarters(calendar: CalendarFirstDay | null, history: AssistantHistoryView | null, now: Date, text: StarterText): FirstDayStarter[] {
    const starters: FirstDayStarter[] = [];
    const meeting = meetingStarter(calendar, text);
    if (meeting) starters.push({ id: "meeting", title: meeting, source: text.fromCalendar });
    const project = projectWithProblem(history);
    if (project) {
        const day = new Date(project.openProblemAt || project.lastAt).toLocaleDateString(text.language, { weekday: "long" });
        starters.push({ id: "problem", title: project.openQuestion, source: text.t("firstDay.fromSession", { day, assistant: assistantName(project) }) });
    }
    const free = (calendar?.connected ? calendar.freeRanges ?? [] : []).filter((range) => range.end - range.start >= HOUR_MS);
    if (free.length > 0) {
        const ranges = free.map((range) => text.t("firstDay.range", { start: text.clock(range.start), end: text.clock(range.end) })).join(text.t("firstDay.rangesJoin"));
        starters.push({ id: "plan", title: text.t(now.getHours() >= 12 ? "firstDay.planAfternoon" : "firstDay.planDay"), source: text.t("firstDay.free", { ranges }) });
    }
    return starters.slice(0, 3);
}
