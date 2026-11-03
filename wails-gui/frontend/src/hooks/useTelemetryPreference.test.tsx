import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useTelemetryPreference } from "./useTelemetryPreference";

const mocks = vi.hoisted(() => ({
    getTier: vi.fn<() => Promise<string>>(),
    setTier: vi.fn<(tier: string) => Promise<void>>(),
    listeners: new Map<string, (tier: string) => void>(),
    unsubscribe: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetTelemetryTier: mocks.getTier,
    SetTelemetryTier: mocks.setTier,
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, callback: (tier: string) => void) => {
        mocks.listeners.set(name, callback);
        return () => {
            mocks.unsubscribe(name);
            mocks.listeners.delete(name);
        };
    },
}));

function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
}

describe("useTelemetryPreference", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mocks.listeners.clear();
        mocks.getTier.mockResolvedValue("detailed");
        mocks.setTier.mockResolvedValue(undefined);
    });

    afterEach(() => cleanup());

    it("preserves a saved tier event when the initial read arrives later", async () => {
        const read = deferred<string>();
        mocks.getTier.mockReturnValue(read.promise);
        const { result } = renderHook(() => useTelemetryPreference());
        expect(result.current.loading).toBe(true);

        act(() => mocks.listeners.get("telemetry:tier-changed")!("off"));
        expect(result.current.enabled).toBe(false);
        expect(result.current.loading).toBe(false);

        await act(async () => read.resolve("detailed"));

        expect(result.current.enabled).toBe(false);
        expect(mocks.setTier).not.toHaveBeenCalled();
    });

    it("keeps the newest saved tier event when an earlier save resolves later", async () => {
        const save = deferred<void>();
        mocks.setTier.mockReturnValue(save.promise);
        const { result } = renderHook(() => useTelemetryPreference());
        await waitFor(() => expect(result.current.loading).toBe(false));

        let saving!: Promise<void>;
        act(() => { saving = result.current.setEnabled(false); });
        expect(result.current.saving).toBe(true);
        expect(mocks.setTier).toHaveBeenCalledWith("off");

        act(() => {
            mocks.listeners.get("telemetry:tier-changed")!("off");
            mocks.listeners.get("telemetry:tier-changed")!("detailed");
        });
        await act(async () => {
            save.resolve();
            await saving;
        });

        expect(result.current.enabled).toBe(true);
        expect(result.current.saving).toBe(false);
    });

    it("allows an explicit opt-out after the saved preference cannot be read", async () => {
        mocks.getTier.mockRejectedValue(new Error("Read failed"));
        const { result } = renderHook(() => useTelemetryPreference());
        await waitFor(() => expect(result.current.loading).toBe(false));
        expect(result.current.enabled).toBe(true);

        await act(async () => result.current.setEnabled(false));

        expect(mocks.setTier).toHaveBeenCalledWith("off");
        expect(result.current.enabled).toBe(false);
        expect(result.current.saving).toBe(false);
    });

    it("removes its subscription and ignores delayed reads and events after unmount", async () => {
        const read = deferred<string>();
        mocks.getTier.mockReturnValue(read.promise);
        const { result, unmount } = renderHook(() => useTelemetryPreference());
        const listener = mocks.listeners.get("telemetry:tier-changed")!;
        const lastState = result.current;

        unmount();

        expect(mocks.unsubscribe).toHaveBeenCalledTimes(1);
        expect(mocks.unsubscribe).toHaveBeenCalledWith("telemetry:tier-changed");
        expect(mocks.listeners.has("telemetry:tier-changed")).toBe(false);
        await act(async () => {
            listener("off");
            read.resolve("off");
        });
        expect(result.current).toBe(lastState);
        expect(mocks.setTier).not.toHaveBeenCalled();
    });
});
