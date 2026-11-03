import { resetPageQueries } from '@/lib/page-queries';
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { BRIEF_RESYNC_MS, useLatestBrief } from "./useLatestBrief";

const { getLatest, getByID, generate, events } = vi.hoisted(() => ({
    getLatest: vi.fn(),
    getByID: vi.fn(),
    generate: vi.fn(),
    events: new Map<string, (data?: unknown) => void>(),
}));
vi.mock("@/lib/wails-shim-app", () => ({ GetLatestBrief: getLatest, GetTodayBriefByID: getByID, GenerateMobileBriefNow: generate }));
vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, callback: (data?: unknown) => void) => {
        events.set(name, callback);
        return () => events.delete(name);
    },
}));

beforeEach(() => {
    resetPageQueries();
    events.clear();
    getLatest.mockReset().mockResolvedValue({ brief: null, building: false });
    getByID.mockReset();
    generate.mockReset();
});

describe("Today's stored brief refresh", () => {
    it("keeps the shown brief while a changed memory is rechecked", async () => {
        getLatest.mockResolvedValueOnce({ brief: { briefId: "old", narrative: [{ text: "PT" }] }, building: false });
        let complete!: (value: unknown) => void;
        getLatest.mockReturnValueOnce(new Promise((resolve) => { complete = resolve; }));
        const { result } = renderHook(() => useLatestBrief(true));
        await waitFor(() => expect(result.current.view?.brief?.briefId).toBe("old"));
        act(() => { events.get("memory:updated")?.(); });
        expect(result.current.view?.brief?.briefId).toBe("old");
        await act(async () => complete({ brief: { briefId: "safe" }, building: false }));
        expect(result.current.view?.brief?.briefId).toBe("safe");
    });
    it("quietly retries one failed read before showing a failure", async () => {
        getLatest.mockRejectedValueOnce(new Error("transient")).mockResolvedValueOnce({ brief: { briefId: "recovered" }, building: false });
        const { result } = renderHook(() => useLatestBrief(true));
        await waitFor(() => expect(result.current.view?.brief?.briefId).toBe("recovered"));
        expect(getLatest).toHaveBeenCalledTimes(2);
        expect(result.current.loadFailed).toBe(false);
    });

    it("shows failure only after both attempts fail", async () => {
        getLatest.mockRejectedValue(new Error("offline"));
        const { result } = renderHook(() => useLatestBrief(true));
        await waitFor(() => expect(result.current.loadFailed).toBe(true));
        expect(getLatest).toHaveBeenCalledTimes(2);
    });

    it("does not retry a read after the page is withdrawn", async () => {
        let reject!: (error: Error) => void;
        getLatest.mockReturnValueOnce(new Promise((_resolve, fail) => { reject = fail; }));
        const { unmount } = renderHook(() => useLatestBrief(true));
        await waitFor(() => expect(getLatest).toHaveBeenCalledTimes(1));
        unmount();
        await act(async () => reject(new Error("late")));
        expect(getLatest).toHaveBeenCalledTimes(1);
    });

    it("opens the source brief when it exists and falls back to latest when it does not", async () => {
        const selected = { brief: { briefId: "2026-10-23-evening" }, building: false };
        getByID.mockImplementation(async (briefId: string) => briefId === "2026-10-23-evening"
            ? selected
            : { brief: null, building: false });
        getLatest.mockResolvedValue({ brief: { briefId: "today-morning" }, building: false });

        const { result, rerender } = renderHook(
            ({ briefId, nonce }) => useLatestBrief(true, briefId, nonce),
            { initialProps: { briefId: "2026-10-23-evening", nonce: 1 } },
        );
        await waitFor(() => expect(result.current.view).toEqual(selected));
        await act(async () => { rerender({ briefId: "missing-source-brief", nonce: 2 }); });
        await waitFor(() => expect(getByID).toHaveBeenCalledWith("missing-source-brief"));
        await waitFor(() => expect(result.current.view).toEqual({ brief: { briefId: "today-morning" }, building: false }));
        expect(getByID).toHaveBeenCalledWith("2026-10-23-evening");
        expect(getLatest).toHaveBeenCalled();
    });

    // Devin's review of #273: a brief made after an older one was opened from
    // a Your week notice stayed hidden behind it until Today remounted.
    it("shows a newly stored brief after an older one was opened, and keeps the older one on its own updates", async () => {
        const older = { brief: { briefId: "2026-10-23-evening" }, building: false };
        const fresh = { brief: { briefId: "2026-10-27-morning" }, building: false };
        getByID.mockResolvedValue(older);
        getLatest.mockResolvedValue(fresh);
        const { result, rerender } = renderHook(
            ({ briefId, nonce }) => useLatestBrief(true, briefId, nonce),
            { initialProps: { briefId: "2026-10-23-evening", nonce: 1 } },
        );
        await waitFor(() => expect(result.current.view).toEqual(older));
        // The older brief itself was updated (its audio): it stays open.
        const opened = getByID.mock.calls.length;
        await act(async () => { await events.get("brief:stored")?.("2026-10-23-evening"); });
        await waitFor(() => expect(getByID.mock.calls.length).toBeGreaterThan(opened));
        expect(result.current.view).toEqual(older);
        // A new brief was stored: Today shows it.
        await act(async () => { await events.get("brief:stored")?.("2026-10-27-morning"); });
        await waitFor(() => expect(result.current.view).toEqual(fresh));
        // Later reads stay on the latest until a new request opens a brief.
        const following = getByID.mock.calls.length;
        await act(async () => { await events.get("mobile:updated")?.(); });
        expect(getByID.mock.calls.length).toBe(following);
        expect(result.current.view).toEqual(fresh);
        await act(async () => { rerender({ briefId: "2026-10-23-evening", nonce: 2 }); });
        await waitFor(() => expect(getByID.mock.calls.length).toBeGreaterThan(following));
        await waitFor(() => expect(result.current.view).toEqual(older));
    });

    it.each(["cron:state", "handoff:created", "handoff:updated", "insight:saved", "memory:updated", "activity:summary-ready", "deliverables:updated", "agency:updated"])("reads changed stored content on %s and unsubscribes", async (event) => {
        const { result, unmount } = renderHook(() => useLatestBrief(true));
        await waitFor(() => expect(result.current.loading).toBe(false));
        const updated = { brief: { briefId: "same-morning-brief" }, dailySummary: { text: "Updated during the day" }, building: false };
        getLatest.mockResolvedValue(updated);
        await act(async () => { await events.get(event)?.(); });
        await waitFor(() => expect(result.current.view).toEqual(updated));
        expect(getLatest).toHaveBeenCalledTimes(2);
        unmount();
        await act(async () => { events.get(event)?.(); });
        expect(getLatest).toHaveBeenCalledTimes(2);
    });

    // Snappy 1.0: showing the page paints what Today has; it reads again
    // only once the brief is past its 15-minute budget, and never while
    // hidden or unmounted.
    it("refreshes on returning to a visible page past its budget, but not when hidden or unmounted", async () => {
        const { result, unmount } = renderHook(() => useLatestBrief(true));
        await waitFor(() => expect(result.current.loading).toBe(false));
        const visible = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
        act(() => document.dispatchEvent(new Event("visibilitychange")));
        expect(getLatest).toHaveBeenCalledTimes(1);
        visible.mockReturnValue("visible");
        act(() => document.dispatchEvent(new Event("visibilitychange")));
        expect(getLatest).toHaveBeenCalledTimes(1);
        const now = vi.spyOn(Date, "now").mockReturnValue(Date.now() + BRIEF_RESYNC_MS + 1);
        act(() => document.dispatchEvent(new Event("visibilitychange")));
        await waitFor(() => expect(getLatest).toHaveBeenCalledTimes(2));
        unmount();
        act(() => document.dispatchEvent(new Event("visibilitychange")));
        expect(getLatest).toHaveBeenCalledTimes(2);
        now.mockRestore();
        visible.mockRestore();
    });
});

// Snappy 1.0: "Make my brief now" follows the build's progress event, not a
// 4-second poll.
describe("Making a brief", () => {
    it("never polls while making, and says it failed when the build stops without a brief", async () => {
        generate.mockResolvedValue({ status: "started" });
        getLatest.mockResolvedValue({ brief: null, building: true });
        const { result } = renderHook(() => useLatestBrief(true));
        await waitFor(() => expect(result.current.loading).toBe(false));
        vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
        await act(async () => { await result.current.makeNow(); });
        expect(result.current.make).toBe("making");
        const reads = getLatest.mock.calls.length;
        await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
        expect(getLatest.mock.calls.length).toBe(reads);
        vi.useRealTimers();
        getLatest.mockResolvedValue({ brief: null, building: false });
        await act(async () => { await events.get("brief:building")?.(false); });
        await waitFor(() => expect(result.current.make).toBe("failed"));
    });

    it("stays making on the build's start and on a read before it stops", async () => {
        generate.mockResolvedValue({ status: "started" });
        getLatest.mockResolvedValue({ brief: null, building: false });
        const { result } = renderHook(() => useLatestBrief(true));
        await waitFor(() => expect(result.current.loading).toBe(false));
        await act(async () => { await result.current.makeNow(); });
        await act(async () => { await events.get("brief:building")?.(true); });
        await act(async () => { await events.get("mobile:updated")?.(); });
        expect(result.current.make).toBe("making");
        getLatest.mockResolvedValue({ brief: { briefId: "2026-10-27-on_request" }, building: false });
        await act(async () => { await events.get("brief:stored")?.("2026-10-27-on_request"); });
        await waitFor(() => expect(result.current.make).toBe("idle"));
        expect(result.current.view).toEqual({ brief: { briefId: "2026-10-27-on_request" }, building: false });
    });
});
