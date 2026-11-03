import { resetPageQueries } from '@/lib/page-queries';
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ getGoalCheckIn: vi.fn(), handlers: new Map<string, () => void>() }));
vi.mock("@/lib/wails-shim-app", () => ({ GetGoalCheckIn: mocks.getGoalCheckIn }));
vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, handler: () => void) => {
        mocks.handlers.set(name, handler);
        return () => mocks.handlers.delete(name);
    },
}));

import { useGoalCheckIn } from "./useGoalCheckIn";

const checkIn = { goalId: "g", eventId: "e", title: "Discovery", dueAt: "2026-08-01T23:59:59Z" };

describe("useGoalCheckIn", () => {
    beforeEach(() => {
        resetPageQueries();
        mocks.getGoalCheckIn.mockReset();
        mocks.handlers.clear();
    });

    it("drops the check-in when observation is paused and brings it back on resume", async () => {
        mocks.getGoalCheckIn.mockResolvedValueOnce(checkIn);
        const { result } = renderHook(() => useGoalCheckIn(true));
        await act(async () => {});
        expect(result.current.checkIn).toEqual(checkIn);

        // Pausing from the tray or the hotkey: the computer now asks nothing.
        mocks.getGoalCheckIn.mockResolvedValueOnce(null);
        await act(async () => { mocks.handlers.get("closed-eyes:changed")?.(); });
        expect(result.current.checkIn).toBeNull();

        mocks.getGoalCheckIn.mockResolvedValueOnce(checkIn);
        await act(async () => { mocks.handlers.get("closed-eyes:changed")?.(); });
        expect(result.current.checkIn).toEqual(checkIn);
    });
});
