import { resetPageQueries } from '@/lib/page-queries';
import { act, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import zh from "@/i18n/locales/zh-CN/settings.json";
import { NOT_READY_RETRY_MS } from "@/hooks/useCaptureCounts";
import { CapturedCountsCard } from "./captured-counts-card";

const mocks = vi.hoisted(() => ({
    handlers: new Map<string, Set<(...args: unknown[]) => void>>(),
    captureCounts: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetCaptureCounts: mocks.captureCounts,
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, handler: (...args: unknown[]) => void) => {
        const set = mocks.handlers.get(name) ?? new Set();
        set.add(handler);
        mocks.handlers.set(name, set);
        return () => set.delete(handler);
    },
}));

function emit(name: string, ...args: unknown[]) {
    act(() => {
        for (const handler of mocks.handlers.get(name) ?? []) handler(...args);
    });
}

const counts = {
    paused: false,
    notReady: false,
    todayStart: 0,
    weekStart: 0,
    sources: [
        { source: "screens", today: 412, week: 2903 },
        { source: "textRead", today: 120, week: 840 },
        { source: "audioMinutes", today: 38, week: 211 },
        { source: "meetings", today: 1, week: 6 },
        { source: "typing", today: 54, week: 300 },
        { source: "calendarEvents", today: 9, week: 31 },
        { source: "calendarPeeks", today: 0, week: 2 },
        // A source this build has no words for stays hidden.
        { source: "clipboard", today: 5, week: 5 },
    ],
};

function cells(rowName: RegExp): string[] {
    const row = screen.getByRole("row", { name: rowName });
    return within(row)
        .getAllByRole("cell")
        .map((cell) => cell.textContent ?? "");
}

describe("CapturedCountsCard", () => {
    beforeEach(async () => {
        resetPageQueries();
        vi.clearAllMocks();
        mocks.handlers.clear();
        await i18n.changeLanguage("en");
    });

    it("shows one row per source with today and the last 7 days, right-aligned", async () => {
        mocks.captureCounts.mockResolvedValue(counts);
        render(<CapturedCountsCard />);

        expect(screen.getByText("What Isoform captured")).toBeInTheDocument();
        expect(
            screen.getByText("What Isoform saved on this computer, counted for today and for the last 7 days."),
        ).toBeInTheDocument();
        await screen.findByTestId("captured-counts");

        expect(screen.getByRole("columnheader", { name: "Today" })).toHaveClass("text-right");
        expect(screen.getByRole("columnheader", { name: "Last 7 days" })).toHaveClass("text-right");
        expect(cells(/Screens seen/)).toEqual(["412", "2,903"]);
        expect(cells(/Screens read for text/)).toEqual(["120", "840"]);
        expect(cells(/Minutes with speech transcribed/)).toEqual(["38", "211"]);
        expect(cells(/Meeting transcripts/)).toEqual(["1", "6"]);
        expect(cells(/Typed text saved/)).toEqual(["54", "300"]);
        expect(cells(/Calendar events read/)).toEqual(["9", "31"]);
        expect(cells(/Web calendar checks/)).toEqual(["0", "2"]);
        // Seven sources plus the header row; the unknown source is left out.
        expect(screen.getAllByRole("row")).toHaveLength(8);
        for (const cell of screen.getAllByRole("cell")) {
            expect(cell).toHaveClass("text-right", "tabular-nums");
        }

        // Local midnight goes to Go as the start of today.
        const since = mocks.captureCounts.mock.calls[0][0] as number;
        const midnight = new Date();
        midnight.setHours(0, 0, 0, 0);
        expect(since).toBe(midnight.getTime());
    });

    it("says Paused instead of counts while observing is paused, and refreshes on resume", async () => {
        mocks.captureCounts.mockResolvedValue({ paused: true, notReady: false, todayStart: 0, weekStart: 0, sources: null });
        render(<CapturedCountsCard />);

        expect(await screen.findByText("Paused. Isoform saves nothing until you resume observing.")).toBeInTheDocument();
        expect(screen.queryByTestId("captured-counts")).not.toBeInTheDocument();

        mocks.captureCounts.mockResolvedValue(counts);
        emit("closed-eyes:changed", false);
        await screen.findByTestId("captured-counts");
        expect(screen.queryByText(/^Paused/)).not.toBeInTheDocument();
    });

    it("shows no numbers while activity.db is opening, then asks again", async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            mocks.captureCounts.mockResolvedValueOnce({ paused: false, notReady: true, todayStart: 0, weekStart: 0, sources: null });
            mocks.captureCounts.mockResolvedValue(counts);
            render(<CapturedCountsCard />);
            await waitFor(() => expect(mocks.captureCounts).toHaveBeenCalledTimes(1));
            await act(async () => {
                await Promise.resolve();
            });
            expect(screen.queryByTestId("captured-counts")).not.toBeInTheDocument();
            expect(screen.queryByText(/^Paused|could not be read/)).not.toBeInTheDocument();

            await act(async () => {
                vi.advanceTimersByTime(NOT_READY_RETRY_MS);
            });
            await screen.findByTestId("captured-counts");
            expect(mocks.captureCounts).toHaveBeenCalledTimes(2);
        } finally {
            vi.useRealTimers();
        }
    });

    it("runs one refresh for events that land together", async () => {
        mocks.captureCounts.mockResolvedValue(counts);
        render(<CapturedCountsCard />);
        await screen.findByTestId("captured-counts");

        emit("activity:session-saved", "s1");
        emit("calendar:synced");
        act(() => {
            window.dispatchEvent(new Event("focus"));
        });
        await waitFor(() => expect(mocks.captureCounts).toHaveBeenCalledTimes(2));
        await new Promise((resolve) => setTimeout(resolve, 600));
        expect(mocks.captureCounts).toHaveBeenCalledTimes(2);
    });

    it("refreshes when a session is saved and says when the counts could not be read", async () => {
        mocks.captureCounts.mockResolvedValue(counts);
        render(<CapturedCountsCard />);
        await screen.findByTestId("captured-counts");

        mocks.captureCounts.mockRejectedValue(new Error("database is locked"));
        emit("activity:session-saved", "s1");
        expect(
            await screen.findByText("The counts could not be read. Reopen Settings to try again."),
        ).toBeInTheDocument();
        expect(screen.queryByTestId("captured-counts")).not.toBeInTheDocument();
        expect(mocks.captureCounts).toHaveBeenCalledTimes(2);
    });

    it("uses the Chinese copy", async () => {
        await i18n.changeLanguage("zh-CN");
        mocks.captureCounts.mockResolvedValue(counts);
        render(<CapturedCountsCard />);
        await screen.findByTestId("captured-counts");
        expect(screen.getByText(zh.observation.captured.title)).toBeInTheDocument();
        expect(screen.getByText(zh.observation.captured.sources.audioMinutes)).toBeInTheDocument();
        await waitFor(() => expect(cells(new RegExp(zh.observation.captured.sources.screens))).toEqual(["412", "2,903"]));
    });
});
