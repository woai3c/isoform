import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetPageQueries } from "@/lib/page-queries";
import type { TodayBeforeYouTalk } from "@/lib/wails-shim-app";
import { useTodayBeforeYouTalk } from "./useTodayBeforeYouTalk";
import { CHANGE_READ_COALESCE_MS } from "./useChangeDrivenRead";

const runtime = vi.hoisted(() => ({ listeners: new Map<string, () => unknown>() }));
vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (event: string, listener: () => unknown) => {
        runtime.listeners.set(event, listener);
        return () => runtime.listeners.delete(event);
    },
}));

const originalGo = (window as any).go;
const meeting: TodayBeforeYouTalk = {
    meetingId: "next", meetingTitle: "Coffee", startAt: Date.now() + 60_000, endAt: Date.now() + 120_000,
    personId: "mei", personName: "Mei Chen",
};

beforeEach(() => {
    resetPageQueries();
    runtime.listeners.clear();
});

afterEach(() => {
    cleanup();
    resetPageQueries();
    vi.useRealTimers();
    (window as any).go = originalGo;
});

describe("useTodayBeforeYouTalk", () => {
    it("stays empty when there is no eligible card and reads nothing while hidden", async () => {
        const get = vi.fn(async () => null);
        (window as any).go = { main: { App: { GetTodayBeforeYouTalk: get } } };
        const visible = renderHook(() => useTodayBeforeYouTalk(true));
        await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
        expect(visible.result.current.card).toBeNull();
        visible.unmount();
        const hidden = renderHook(() => useTodayBeforeYouTalk(false));
        expect(hidden.result.current.card).toBeNull();
        expect(get).toHaveBeenCalledTimes(1);
    });

    it("fails closed without replacing the rest of Today", async () => {
        (window as any).go = { main: { App: { GetTodayBeforeYouTalk: vi.fn(async () => { throw new Error("offline"); }) } } };
        const hook = renderHook(() => useTodayBeforeYouTalk(true));
        await waitFor(() => expect(hook.result.current.card).toBeNull());
    });

    // Was a 30 s poll that removed the card at the meeting's end (2026-11-04):
    // the meeting-prep boundary is now a backend event, so there is no timer.
    it("removes the card when the backend announces the meeting prep boundary, not on a timer", async () => {
        vi.useFakeTimers();
        const get = vi.fn().mockResolvedValueOnce(meeting).mockResolvedValue(null);
        (window as any).go = { main: { App: { GetTodayBeforeYouTalk: get } } };
        const hook = renderHook(() => useTodayBeforeYouTalk(true));
        // The mount read is one coalesced read after mount.
        await act(async () => {
            vi.advanceTimersByTime(CHANGE_READ_COALESCE_MS);
            await Promise.resolve();
            await Promise.resolve();
        });
        expect(hook.result.current.card?.meetingId).toBe("next");
        await act(async () => {
            vi.advanceTimersByTime(30_000);
            await Promise.resolve();
        });
        expect(get).toHaveBeenCalledTimes(1);
        await act(async () => {
            runtime.listeners.get("today-before-you-talk:updated")?.();
            vi.advanceTimersByTime(CHANGE_READ_COALESCE_MS);
            await Promise.resolve();
            await Promise.resolve();
        });
        expect(hook.result.current.card).toBeNull();
        expect(get).toHaveBeenCalledTimes(2);
    });

    it("dismisses only its meeting and immediately shows the next eligible one", async () => {
        const next = { ...meeting, meetingId: "later", personName: "Dana" };
        const get = vi.fn().mockResolvedValueOnce(meeting).mockResolvedValue(next);
        const dismiss = vi.fn(async () => undefined);
        (window as any).go = { main: { App: { GetTodayBeforeYouTalk: get, DismissTodayBeforeYouTalk: dismiss } } };
        const hook = renderHook(() => useTodayBeforeYouTalk(true));
        await waitFor(() => expect(hook.result.current.card?.meetingId).toBe("next"));
        await act(async () => { await hook.result.current.dismiss("next"); });
        expect(dismiss).toHaveBeenCalledWith("next");
        expect(hook.result.current.card?.meetingId).toBe("later");
    });
});
