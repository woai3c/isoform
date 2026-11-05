import { renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";

const reads = vi.hoisted(() => ({ day: vi.fn().mockResolvedValue(0), history: vi.fn().mockResolvedValue(null), calendar: vi.fn().mockResolvedValue(null) }));
vi.mock("@/lib/page-queries", () => ({
    GetFirstWeekDay: reads.day,
    GetAssistantHistory: reads.history,
    GetCalendarFirstDay: reads.calendar,
    usePageQueryRefresh: () => {},
}));
import { useFirstDay } from "./useFirstDay";

it("calls first-day bindings without promise callback arguments", async () => {
    const hook = renderHook(() => useFirstDay());
    await waitFor(() => expect(hook.result.current.day).toBe(0));
    await waitFor(() => expect(reads.calendar).toHaveBeenCalled());
    expect(reads.day).toHaveBeenCalledWith();
    expect(reads.history).toHaveBeenCalledWith();
    expect(reads.calendar).toHaveBeenCalledWith();
});
