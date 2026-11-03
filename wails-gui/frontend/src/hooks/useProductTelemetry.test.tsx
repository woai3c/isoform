import { act, cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useProductTelemetry } from "./useProductTelemetry";

const mocks = vi.hoisted(() => ({
    tier: vi.fn<() => Promise<string>>(),
    activity: vi.fn(),
    listeners: new Map<string, (value: string) => void>(),
}));
vi.mock("@/lib/wails-shim-app", () => ({
    GetTelemetryTier: mocks.tier,
    RecordProductActivity: mocks.activity,
}));
vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, callback: (value: string) => void) => {
        mocks.listeners.set(name, callback);
        return () => mocks.listeners.delete(name);
    },
}));

function Harness() {
    useProductTelemetry();
    return null;
}

async function start() {
    const result = render(<Harness />);
    await act(async () => {});
    return result;
}

describe("anonymous product activity", () => {
    let focused = true;
    beforeEach(() => {
        vi.useFakeTimers();
        vi.setSystemTime(new Date("2026-10-16T09:00:00Z"));
        focused = true;
        Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
        vi.spyOn(document, "hasFocus").mockImplementation(() => focused);
        mocks.tier.mockResolvedValue("detailed");
        mocks.activity.mockResolvedValue(undefined);
        mocks.activity.mockClear();
        mocks.listeners.clear();
    });
    afterEach(() => {
        cleanup();
        vi.restoreAllMocks();
        vi.unstubAllGlobals();
        vi.useRealTimers();
    });

    it("reports 30 second heartbeat slices with only seconds and a random session ID", async () => {
        await start();
        act(() => vi.advanceTimersByTime(30_000));
        const id = mocks.activity.mock.calls[0][1];
        expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
        expect(mocks.activity.mock.calls).toEqual([[0, id], [30, id]]);
    });

    it("uses cryptographic random bytes when the WebView has no randomUUID API", async () => {
        const getRandomValues = crypto.getRandomValues.bind(crypto);
        vi.stubGlobal("crypto", { getRandomValues });
        await start();
        act(() => vi.advanceTimersByTime(30_000));
        const id = mocks.activity.mock.calls[0][1];
        expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
        expect(mocks.activity.mock.calls).toEqual([[0, id], [30, id]]);
        act(() => {
            vi.advanceTimersByTime(5 * 60_000);
            window.dispatchEvent(new Event("pointerdown"));
        });
        const nextID = mocks.activity.mock.lastCall![1];
        expect(nextID).not.toBe(id);
        expect(nextID).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    });

    it("keeps the window mounted and telemetry closed when Web Crypto is unavailable", async () => {
        vi.stubGlobal("crypto", undefined);
        const view = await start();
        act(() => {
            vi.advanceTimersByTime(60_000);
            window.dispatchEvent(new Event("pointerdown"));
        });
        view.unmount();
        expect(mocks.activity).not.toHaveBeenCalled();
    });

    it("stops at five minutes idle and starts another session on input without reading it", async () => {
        await start();
        act(() => vi.advanceTimersByTime(15 * 60_000));
        expect(mocks.activity.mock.calls.reduce((sum, [seconds]) => sum + seconds, 0)).toBe(300);
        const oldID = mocks.activity.mock.calls[0][1];
        act(() => window.dispatchEvent(new KeyboardEvent("keydown", { key: "private content" })));
        const nextID = mocks.activity.mock.lastCall![1];
        expect(nextID).not.toBe(oldID);
        expect(mocks.activity).toHaveBeenLastCalledWith(0, nextID);
        act(() => vi.advanceTimersByTime(30_000));
        expect(mocks.activity).toHaveBeenLastCalledWith(30, nextID);
    });

    it("flushes the partial slice on blur, excludes background time, and resumes on focus", async () => {
        await start();
        act(() => {
            vi.advanceTimersByTime(12_500);
            focused = false;
            window.dispatchEvent(new Event("blur"));
            vi.advanceTimersByTime(60_000);
        });
        expect(mocks.activity.mock.calls.map(([seconds]) => seconds)).toEqual([0, 12.5]);
        act(() => {
            focused = true;
            window.dispatchEvent(new Event("focus"));
            vi.advanceTimersByTime(10_000);
            window.dispatchEvent(new Event("beforeunload"));
            window.dispatchEvent(new Event("pagehide"));
        });
        expect(mocks.activity.mock.calls.map(([seconds]) => seconds)).toEqual([0, 12.5, 10]);
    });

    it("drops a suspended heartbeat and waits for input to start a new session", async () => {
        await start();
        const oldID = mocks.activity.mock.calls[0][1];
        act(() => {
            vi.setSystemTime(new Date("2026-10-16T11:00:00Z"));
            vi.advanceTimersByTime(30_000);
        });
        expect(mocks.activity.mock.calls.map(([seconds]) => seconds)).toEqual([0]);
        act(() => {
            window.dispatchEvent(new Event("pointerdown"));
            vi.advanceTimersByTime(30_000);
        });
        expect(mocks.activity.mock.calls.map(([seconds]) => seconds)).toEqual([0, 0, 30]);
        expect(mocks.activity.mock.lastCall![1]).not.toBe(oldID);
    });

    it("never reports while sharing is off, including input and unmount", async () => {
        mocks.tier.mockResolvedValue("off");
        const view = await start();
        act(() => {
            window.dispatchEvent(new Event("pointerdown"));
            vi.advanceTimersByTime(600_000);
            window.dispatchEvent(new Event("beforeunload"));
        });
        view.unmount();
        expect(mocks.activity).not.toHaveBeenCalled();
    });

    it("drops an unfinished slice on opt-out and starts fresh on opt-in", async () => {
        await start();
        act(() => {
            vi.advanceTimersByTime(10_000);
            mocks.listeners.get("telemetry:tier-changed")!("off");
        });
        act(() => vi.advanceTimersByTime(60_000));
        expect(mocks.activity.mock.calls.map(([seconds]) => seconds)).toEqual([0]);
        act(() => mocks.listeners.get("telemetry:tier-changed")!("detailed"));
        act(() => vi.advanceTimersByTime(30_000));
        expect(mocks.activity.mock.calls.map(([seconds]) => seconds)).toEqual([0, 0, 30]);
    });

    it("fails closed and does not let a stale preference read reverse opt-out", async () => {
        let resolveTier!: (tier: string) => void;
        mocks.tier.mockReturnValue(new Promise((resolve) => { resolveTier = resolve; }));
        await start();
        act(() => mocks.listeners.get("telemetry:tier-changed")!("off"));
        await act(async () => resolveTier("detailed"));
        act(() => vi.advanceTimersByTime(60_000));
        expect(mocks.activity).not.toHaveBeenCalled();
    });

    it("flushes the final partial slice when the renderer unmounts", async () => {
        const view = await start();
        act(() => vi.advanceTimersByTime(9_000));
        view.unmount();
        expect(mocks.activity.mock.calls.map(([seconds]) => seconds)).toEqual([0, 9]);
    });
});
