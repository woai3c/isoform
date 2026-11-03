import { act, cleanup, render } from "@testing-library/react";
import type { PostHogConfig } from "posthog-js";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
    tier: "detailed",
    getTelemetryTier: vi.fn<() => Promise<string>>(),
    listeners: new Map<string, (...args: unknown[]) => void>(),
    posthog: {
        __loaded: false,
        init: vi.fn(),
        _send_request: vi.fn(),
        _handle_unload: vi.fn(),
        __request_queue: [] as unknown[],
        register: vi.fn(),
        set_config: vi.fn(),
        capture: vi.fn(),
        identify: vi.fn(),
        reset: vi.fn(),
        opt_out_capturing: vi.fn(),
        opt_in_capturing: vi.fn(),
        has_opted_out_capturing: vi.fn(() => false),
    },
}));

vi.mock("posthog-js", () => ({ default: mocks.posthog }));

vi.mock("@/lib/wails-shim-app", () => ({
    GetWebBaseURL: vi.fn(() => Promise.resolve("https://app.example.com")),
    GetVersion: vi.fn(() => Promise.resolve("1.2.3")),
    GetTelemetryTier: () => mocks.getTelemetryTier(),
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: vi.fn((name: string, callback: (...args: unknown[]) => void) => {
        mocks.listeners.set(name, callback);
        return () => mocks.listeners.delete(name);
    }),
}));

async function renderHook() {
    const { usePostHogIdentify } = await import("./usePostHogIdentify");
    function Harness() {
        usePostHogIdentify(true);
        return null;
    }
    render(<Harness />);
    await act(async () => {
        await Promise.resolve();
        await Promise.resolve();
        await Promise.resolve();
    });
}

describe("usePostHogIdentify", () => {
    beforeEach(() => {
        vi.resetModules();
        vi.stubEnv("VITE_PUBLIC_POSTHOG_PROD_KEY", "phc_test");
        vi.stubEnv("VITE_PUBLIC_POSTHOG_PROD_HOST", "https://posthog.example.com");
        mocks.listeners.clear();
        mocks.tier = "detailed";
        mocks.posthog.__loaded = false;
        mocks.posthog._send_request = vi.fn();
        mocks.getTelemetryTier.mockReset();
        mocks.getTelemetryTier.mockImplementation(() => Promise.resolve(mocks.tier));
        Object.values(mocks.posthog).forEach((value) => {
            if (typeof value === "function" && "mockReset" in value) value.mockReset();
        });
        mocks.posthog.init.mockImplementation((_key: string, config: Partial<PostHogConfig>) => {
            mocks.posthog.__loaded = true;
            config.loaded?.(mocks.posthog as never);
        });
        mocks.posthog.has_opted_out_capturing.mockReturnValue(true);
    });

    afterEach(() => {
        cleanup();
        vi.unstubAllEnvs();
    });

    it("sends nothing while diagnostic data is off", async () => {
        mocks.tier = "off";
        await renderHook();

        expect(mocks.posthog.init).not.toHaveBeenCalled();
        expect(mocks.posthog.capture).not.toHaveBeenCalled();
        expect(mocks.posthog.identify).not.toHaveBeenCalled();
    });

    it("starts without click capture or identify when diagnostic data is on", async () => {
        mocks.tier = "detailed";
        await renderHook();

        expect(mocks.posthog.init).toHaveBeenCalledTimes(1);
        expect(mocks.posthog.init.mock.calls[0][1]).toMatchObject({
            autocapture: false,
            capture_pageview: false,
            disable_session_recording: true,
            capture_exceptions: false,
            disable_external_dependency_loading: true,
            advanced_disable_flags: true,
            opt_out_capturing_by_default: true,
        });
        expect(mocks.posthog.reset).toHaveBeenCalledWith(true);
        expect(mocks.posthog.reset.mock.invocationCallOrder[0]).toBeLessThan(
            mocks.posthog.set_config.mock.invocationCallOrder[0],
        );
        expect(mocks.posthog.reset.mock.invocationCallOrder[0]).toBeLessThan(
            mocks.posthog.capture.mock.invocationCallOrder[0],
        );
        expect(mocks.posthog.identify).not.toHaveBeenCalled();
        expect(mocks.posthog.capture).toHaveBeenCalledWith(
            "client.telemetry.ready",
            expect.not.objectContaining({ email: expect.anything() }),
        );
    });

    it("stops capturing when the user turns diagnostic data off", async () => {
        mocks.tier = "detailed";
        await renderHook();
        expect(mocks.posthog.init).toHaveBeenCalledTimes(1);

        await act(async () => {
            mocks.listeners.get("telemetry:tier-changed")?.("off");
            await Promise.resolve();
        });

        expect(mocks.posthog.opt_out_capturing).toHaveBeenCalledTimes(1);
        expect(mocks.posthog.reset).toHaveBeenCalled();
    });

    it("resets identity before opting back in after sharing is enabled again", async () => {
        await renderHook();
        await act(async () => {
            mocks.listeners.get("telemetry:tier-changed")?.("off");
        });
        mocks.posthog.reset.mockClear();
        mocks.posthog.opt_in_capturing.mockClear();
        mocks.posthog.capture.mockClear();

        await act(async () => {
            mocks.listeners.get("telemetry:tier-changed")?.("detailed");
            await Promise.resolve();
            await Promise.resolve();
        });

        expect(mocks.posthog.init).toHaveBeenCalledTimes(2);
        expect(mocks.posthog.reset).toHaveBeenCalledWith(true);
        expect(mocks.posthog.opt_in_capturing).toHaveBeenCalledWith({ captureEventName: false });
        expect(mocks.posthog.reset.mock.invocationCallOrder[0]).toBeLessThan(
            mocks.posthog.opt_in_capturing.mock.invocationCallOrder[0],
        );
        expect(mocks.posthog.opt_in_capturing.mock.invocationCallOrder[0]).toBeLessThan(
            mocks.posthog.capture.mock.invocationCallOrder[0],
        );
    });

    it("drops startup captures until persisted identity has been cleared", async () => {
        const event = { uuid: "startup-event", event: "$exception", properties: { distinct_id: "Legacy User <legacy@example.com>" } };
        let gate: PostHogConfig["before_send"];
        mocks.posthog.init.mockImplementation((_key: string, config: Partial<PostHogConfig>) => {
            gate = config.before_send;
            expect(typeof gate).toBe("function");
            if (typeof gate !== "function") throw new Error("missing capture gate");
            expect(gate(event)).toBeNull();
            config.loaded?.(mocks.posthog as never);
            expect(gate(event)).toBe(event);
        });

        await renderHook();
        expect(mocks.posthog.reset).toHaveBeenCalledWith(true);
        expect(mocks.posthog.capture).toHaveBeenCalledTimes(1);
    });

    it("stays closed when the SDK cannot clear persisted identity", async () => {
        mocks.posthog.reset.mockImplementation(() => { throw new Error("reset failed"); });
        mocks.posthog.init.mockImplementation((_key: string, config: Partial<PostHogConfig>) => {
            // The SDK catches errors thrown by its loaded callback.
            try {
                config.loaded?.(mocks.posthog as never);
            } catch {
                // Keep the startup gate closed, just as the SDK does.
            }
            const gate = config.before_send;
            if (typeof gate !== "function") throw new Error("missing capture gate");
            expect(gate({ uuid: "startup-event", event: "$exception", properties: {} })).toBeNull();
        });

        await renderHook();
        expect(mocks.posthog.opt_in_capturing).not.toHaveBeenCalled();
        expect(mocks.posthog.set_config).not.toHaveBeenCalled();
        expect(mocks.posthog.capture).not.toHaveBeenCalled();
    });

    it("ignores a stale initial preference after an opt-out event", async () => {
        let resolveTier!: (tier: string) => void;
        mocks.getTelemetryTier.mockReturnValue(new Promise((resolve) => { resolveTier = resolve; }));
        await renderHook();
        await act(async () => {
            mocks.listeners.get("telemetry:tier-changed")?.("off");
            resolveTier("detailed");
        });

        expect(mocks.posthog.init).not.toHaveBeenCalled();
        expect(mocks.posthog.capture).not.toHaveBeenCalled();
    });
});
