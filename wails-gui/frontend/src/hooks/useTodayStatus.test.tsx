import { resetPageQueries } from '@/lib/page-queries';
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useTodayStatus } from "./useTodayStatus";

const mocks = vi.hoisted(() => ({
    week: vi.fn(),
    health: vi.fn(),
    eventsOn: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetEngagementWeekSummary: mocks.week,
    GetMobileBriefHealth: mocks.health,
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: mocks.eventsOn,
}));

const weekView = { available: true, sent: 3, actedOn: 2, days: 7, kinds: [] };
const healthView = {
    enabled: true, lastRunAt: "", lastSlot: "", nextRunAt: "", nextSlot: "",
    missedRuns: 0, failingSlot: "", failedAttempts: 0, latestBriefId: "",
};

describe("useTodayStatus", () => {
    let unsubscribe: ReturnType<typeof vi.fn>;
    let listener: (() => void) | null;

    beforeEach(() => {
    resetPageQueries();
        vi.clearAllMocks();
        unsubscribe = vi.fn();
        listener = null;
        mocks.eventsOn.mockImplementation((name: string, cb: () => void) => {
            if (name === "mobile:updated") listener = cb;
            return unsubscribe;
        });
        mocks.week.mockResolvedValue(weekView);
        mocks.health.mockResolvedValue(healthView);
    });

    it("reads both halves, keeps the other when one binding fails, and refreshes on the brief event", async () => {
        mocks.health.mockRejectedValueOnce(new Error("not ready"));
        const { result, unmount } = renderHook(() => useTodayStatus(true));
        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.week).toEqual(weekView);
        expect(result.current.brief).toBeNull();
        expect(mocks.eventsOn).toHaveBeenCalledWith("mobile:updated", expect.any(Function));

        await act(async () => { await listener?.(); });
        await waitFor(() => expect(result.current.brief).toEqual(healthView));

        unmount();
        const calls = mocks.week.mock.calls.length;
        await act(async () => listener?.());
        expect(mocks.week).toHaveBeenCalledTimes(calls);
    });

    it("drops an older answer that resolves after a newer refresh", async () => {
        let resolveFirst: (value: typeof weekView) => void = () => {};
        mocks.week.mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve; }));
        const { result } = renderHook(() => useTodayStatus(true));
        await waitFor(() => expect(mocks.week).toHaveBeenCalledTimes(1));
        mocks.week.mockResolvedValue({ ...weekView, sent: 9 });
        act(() => result.current.refresh());
        await waitFor(() => expect(result.current.week?.sent).toBe(9));

        await act(async () => {
            resolveFirst({ ...weekView, sent: 1 });
            await Promise.resolve();
        });
        expect(result.current.week?.sent).toBe(9);
    });

    it("does nothing while disabled", () => {
        renderHook(() => useTodayStatus(false));
        expect(mocks.week).not.toHaveBeenCalled();
        expect(mocks.eventsOn).not.toHaveBeenCalled();
    });
});
