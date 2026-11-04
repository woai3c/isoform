import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import en from "@/i18n/locales/en/today.json";
import zh from "@/i18n/locales/zh-CN/today.json";
import { TodayStatus, describeBriefTime } from "./TodayStatus";

const telemetry = vi.hoisted(() => ({ record: vi.fn(async () => undefined), finished: null as null | ((token: unknown, success: unknown) => void) }));
vi.mock("@/lib/wails-shim-app", () => ({ RecordTodayAction: telemetry.record }));
vi.mock("../../../wailsjs/runtime/runtime", () => ({ EventsOn: vi.fn((name: string, fn: (token: unknown, success: unknown) => void) => { if (name === "today:brief-request-finished") telemetry.finished = fn; return () => { telemetry.finished = null; }; }) }));
beforeEach(() => { telemetry.record.mockClear(); });

const now = new Date(2026, 9, 14, 12, 0, 0);
const at = (dayOffset: number, hour: number) =>
    new Date(2026, 9, 14 + dayOffset, hour, 2, 0).toISOString();

describe("TodayStatus", () => {
    it("keeps the date, not the tries line or the missed line, while Isoform is still trying (today-review v1.1.20 R45)", () => {
        const pendingAt = new Date(Date.now() - 6 * 3600_000).toISOString();
        const retryAt = new Date(Date.now() + 3600_000).toISOString();
        render(<TodayStatus brief={{ enabled: true, lastRunAt: "", lastSlot: "", nextRunAt: "", nextSlot: "", missedRuns: 0, failingSlot: "", failedAttempts: 0, latestBriefId: "", currentBriefId: "", pendingSlot: "morning", pendingAt, retryAt, pendingAttempts: 9, pendingCause: "timeout" }} date="Tuesday, November 3" />);
        expect(screen.getByTestId("today-status-date").textContent).toBe("Tuesday, November 3");
        expect(screen.getByTestId("today-status")).not.toHaveTextContent(/Tried|tries again/);
        expect(screen.queryByText(/didn't come/)).toBeNull();
    });

    it("does not say the brief missed its time while a try is running", () => {
        render(<TodayStatus date="DATE" building brief={{ enabled: true, lastRunAt: "", lastSlot: "", nextRunAt: at(0, 18), nextSlot: "evening", missedRuns: 0, failingSlot: "morning", failedAttempts: 3, latestBriefId: "", currentBriefId: "", pendingSlot: "morning", pendingAt: at(0, 7), pendingAttempts: 3 }} />);
        expect(screen.getByTestId("today-status")).toHaveTextContent("DATE");
        expect(screen.getByTestId("today-status")).not.toHaveTextContent(/didn't come|next brief/);
    });

    it.each(["en", "zh-CN"])("says what comes next once the slot has given way (today-review v1.1.22 R51) in %s", async (language) => {
        await i18n.changeLanguage(language);
        const now = new Date(2026, 10, 4, 11, 50);
        const nextRunAt = new Date(2026, 10, 4, 18, 0).toISOString();
        const base = { enabled: true, lastRunAt: "", lastSlot: "", nextRunAt, nextSlot: "evening", missedRuns: 0, failingSlot: "", failedAttempts: 0, latestBriefId: "", currentBriefId: "", pendingSlot: "morning", pendingAt: new Date(2026, 10, 4, 7, 0).toISOString(), pendingAttempts: 5, pendingCause: "timeout" };
        const { rerender } = render(<TodayStatus brief={{ ...base, retryAt: "" }} now={now} />);
        expect(screen.queryByTestId("today-status-next")).toBeNull();
        expect(screen.queryByText(/didn't come|没有在/)).toBeNull();
        rerender(<TodayStatus brief={{ ...base, nextRunAt: new Date(2026, 10, 5, 7, 0).toISOString(), nextSlot: "morning", retryAt: "" }} now={new Date(2026, 10, 4, 23, 40)} />);
        expect(screen.getByTestId("today-status-next")).toHaveTextContent(i18n.t("today:status.nextMorningTomorrowAt", { time: "7:00" + (language === "en" ? " AM" : "") }));
        rerender(<TodayStatus brief={{ ...base, retryAt: new Date(2026, 10, 4, 11, 58).toISOString() }} now={now} />);
        expect(screen.queryByTestId("today-status-next")).toBeNull();
        await i18n.changeLanguage("en");
    });

    it("has no Make it now while the brief is missed: the card has the one button (v1.1.5 #2)", async () => {
        await i18n.changeLanguage("en");
        render(<TodayStatus brief={{ enabled: true, lastRunAt: "", lastSlot: "", nextRunAt: "", nextSlot: "", missedRuns: 1, failingSlot: "", failedAttempts: 0, latestBriefId: "", currentBriefId: "", pendingSlot: "morning", pendingAt: at(0, 7) }} now={now} onMissedWhy={vi.fn()} />);
        expect(screen.getByTestId("today-status")).toHaveTextContent("Your morning brief didn't come at 7:02 AM. · Why");
        expect(screen.queryByRole("button", { name: "Make it now" })).not.toBeInTheDocument();
    });
    it("opens the missed brief's own detail from Why (K3)", async () => {
        await i18n.changeLanguage("en");
        const why = vi.fn();
        render(<TodayStatus brief={{ enabled: true, lastRunAt: "", lastSlot: "", nextRunAt: "", nextSlot: "", missedRuns: 1, failingSlot: "", failedAttempts: 0, latestBriefId: "", currentBriefId: "", pendingSlot: "evening", pendingAt: at(0, 18) }} now={now} onMissedWhy={why} />);
        expect(screen.getByTestId("today-status")).toHaveTextContent("Your evening brief didn't come at 6:02 PM. · Why");
        fireEvent.click(screen.getByRole("button", { name: "Why" }));
        expect(why).toHaveBeenCalledWith("evening");
        await waitFor(() => expect(telemetry.record).toHaveBeenCalledWith("today", "T86", "open", "opened", expect.any(Number)));
    });
    it("shows the date and next time without historical missed counts", async () => {
        await i18n.changeLanguage("en");
        render(
            <TodayStatus
                brief={{ latestBriefId: "", currentBriefId: "",
                    enabled: true,
                    lastRunAt: at(0, 7),
                    lastSlot: "morning",
                    nextRunAt: at(0, 18),
                    nextSlot: "evening",
                    missedRuns: 2,
                    failingSlot: "",
                    failedAttempts: 0,
                }}
                now={now} date="Wednesday, October 14"
            />,
        );
        const status = screen.getByTestId("today-status");
        expect(status).toHaveTextContent("Wednesday, October 14 · next brief at 6:02 PM");
        expect(status).not.toHaveTextContent(/Last brief|missed/);
    });

    it("says the first brief is still to come, and hides the next run when the schedule is off", async () => {
        await i18n.changeLanguage("en");
        const { rerender } = render(
            <TodayStatus
                brief={{ latestBriefId: "", currentBriefId: "", enabled: true, lastRunAt: "", lastSlot: "", nextRunAt: at(1, 7), nextSlot: "morning", missedRuns: 0, failingSlot: "", failedAttempts: 0 }}
                now={now}
            />,
        );
        expect(screen.getByTestId("today-status")).toHaveTextContent("next brief tomorrow at 7:02 AM");
        rerender(
            <TodayStatus
                brief={{ latestBriefId: "", currentBriefId: "", enabled: false, lastRunAt: "", lastSlot: "", nextRunAt: at(1, 7), nextSlot: "morning", missedRuns: 0, failingSlot: "", failedAttempts: 0 }}
                now={now}
            />,
        );
        expect(screen.queryByTestId("today-status")).not.toBeInTheDocument();
    });

    it("describes times relative to today, in the one clock", () => {
        const t = (key: string, options?: Record<string, unknown>) => `${key}:${String(options?.time ?? "")}`;
        const clock = (key: string, options?: Record<string, unknown>) => String(i18n.t(key as never, { ...options, ns: "today", lng: "en" }));
        expect(describeBriefTime(at(0, 7), now, t)).toMatch(/^status\.todayAt:/);
        expect(describeBriefTime(at(-1, 18), now, t)).toMatch(/^status\.yesterdayAt:/);
        expect(describeBriefTime(at(1, 7), now, t)).toMatch(/^status\.tomorrowAt:/);
        expect(describeBriefTime(at(-3, 7), now, clock)).toBe("Sun, Oct 11 7:02 AM");
        expect(describeBriefTime("", now, t)).toBeNull();
        expect(describeBriefTime("not a time", now, t)).toBeNull();
    });

    it("no longer says how often Isoform spoke up this week; that line lives in Settings", async () => {
        await i18n.changeLanguage("en");
        render(<TodayStatus brief={{ latestBriefId: "", currentBriefId: "", enabled: true, lastRunAt: at(0, 7), lastSlot: "morning", nextRunAt: "", nextSlot: "", missedRuns: 0, failingSlot: "", failedAttempts: 0 }} now={now} />);
        expect(screen.queryByTestId("today-status")).not.toBeInTheDocument();
        expect((en.status as Record<string, string>).spokeUp_other).toBeUndefined();
    });

    it("adds one muted quiet line per quieted notice kind, only when there is one", async () => {
        await i18n.changeLanguage("en");
        const { rerender } = render(<TodayStatus brief={null} now={now} quiet={[]} />);
        expect(screen.queryByTestId("today-status")).not.toBeInTheDocument();
        rerender(
            <TodayStatus
                brief={null}
                now={now}
                capture={{ text: "Today Isoform saw 466 screens." }}
                quiet={[{ id: "meeting_notes", text: "Meeting notes ready: 3 today, shown here instead of notices." }, { id: "blank", text: "  " }]}
            />,
        );
        const lines = screen.getAllByTestId("today-quiet-line");
        expect(lines.map((line) => line.textContent)).toEqual(["Meeting notes ready: 3 today, shown here instead of notices."]);
        // Under every status line, the capture line included.
        expect(screen.getByTestId("today-capture").compareDocumentPosition(lines[0]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });

    it("keeps the missed-briefs line emphasised and plays nothing in the header", async () => {
        await i18n.changeLanguage("en");
        render(
            <TodayStatus
                brief={{
                    enabled: true, lastRunAt: at(0, 7), lastSlot: "morning", nextRunAt: "", nextSlot: "", missedRuns: 2,
                    failingSlot: "", failedAttempts: 0, latestBriefId: "2026-10-14-morning", currentBriefId: "2026-10-14-morning",
                    audio: { mime: "audio/mp4", bytes: 1200, durationSeconds: 70 },
                }}
                now={now}
            />,
        );
        expect(screen.queryByText(/briefs were missed/)).not.toBeInTheDocument();
        // The spoken brief plays from its own card (TodayBriefSection).
        expect(screen.queryByTestId("brief-audio-toggle")).not.toBeInTheDocument();
    });

    it("has the same status keys in English and Chinese", () => {
        expect(Object.keys(zh.status).sort()).toEqual(Object.keys(en.status).sort());
    });
});
it.each(['en', 'zh-CN'])('shows the current missed slot in %s without historical counts', async (language) => {
    await i18n.changeLanguage(language);
    render(<TodayStatus date="DATE" now={new Date(2026, 9, 14, 13)} brief={{ enabled: true, lastRunAt: at(0, 7), lastSlot: 'morning', nextRunAt: at(0, 18), nextSlot: 'evening', missedRuns: 4, failingSlot: '', failedAttempts: 0, latestBriefId: '', currentBriefId: '', pendingSlot: 'morning', pendingAt: at(0, 7) }} />);
    const status = screen.getByTestId('today-status');
    expect(status).toHaveTextContent(language === 'en' ? "Your morning brief didn't come at" : '早间简报没有在');
    expect(status).not.toHaveTextContent(/DATE|Last brief|7 days/);
    expect(screen.queryByRole('button', { name: language === 'en' ? 'Make it now' : '现在生成' })).not.toBeInTheDocument();
});
