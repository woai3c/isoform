import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useUpcoming } from "./useUpcoming";

const getUpcoming = vi.hoisted(() => vi.fn());
vi.mock("@/lib/wails-shim-app", () => ({ GetUpcoming: getUpcoming }));
vi.mock("../../wailsjs/runtime/runtime", () => ({ EventsOn: () => () => undefined }));

// K6 (T25): a failed Scheduled read is a failure, never an empty list.
describe("useUpcoming", () => {
    it("says a read failed and keeps the last list, then clears on a good read", async () => {
        const item = { id: "job", name: "Weekly digest" };
        getUpcoming.mockResolvedValueOnce([item]).mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce([item]);
        const hook = renderHook(() => useUpcoming(true));
        await waitFor(() => expect(hook.result.current.items).toEqual([item]));
        expect(hook.result.current.failed).toBe(false);
        act(() => hook.result.current.refresh());
        await waitFor(() => expect(hook.result.current.failed).toBe(true));
        expect(hook.result.current.items).toEqual([item]);
        act(() => hook.result.current.refresh());
        await waitFor(() => expect(hook.result.current.failed).toBe(false));
    });
});
