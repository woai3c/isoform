import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useVoiceInput } from "./useVoiceInput";

const mocks = vi.hoisted(() => ({
    init: vi.fn(),
    getStatus: vi.fn(),
    listeners: new Map<string, (payload: unknown) => void>(),
}));

vi.mock("../../wailsjs/go/main/App", () => ({
    GetPlatformPermissions: vi.fn(),
    InitVoiceInput: mocks.init,
    TranscribeAudio: vi.fn(),
    GetVoiceInputStatus: mocks.getStatus,
    OpenPermissionSettings: vi.fn(),
    RequestPermission: vi.fn(),
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, handler: (payload: unknown) => void) => {
        mocks.listeners.set(name, handler);
        return () => mocks.listeners.delete(name);
    },
    LogError: vi.fn(),
    LogInfo: vi.fn(),
}));

const missingModelError =
    "sherpa-onnx assets: model not downloaded; confirm its download in setup or Settings";

beforeEach(() => {
    vi.clearAllMocks();
    mocks.listeners.clear();
    mocks.getStatus.mockResolvedValue({
        available: true,
        ready: false,
        downloading: false,
        building: false,
    });
    mocks.init.mockResolvedValue(undefined);
});

describe("useVoiceInput background initialization", () => {
    it("keeps a missing voice model idle without an error", async () => {
        let rejectInit!: (error: Error) => void;
        mocks.init.mockImplementation(
            () =>
                new Promise<void>((_resolve, reject) => {
                    rejectInit = reject;
                }),
        );

        const { result } = renderHook(() => useVoiceInput());
        await waitFor(() => expect(result.current.state).toBe("initializing"));

        await act(async () => {
            rejectInit(new Error(missingModelError));
        });

        expect(result.current.state).toBe("idle");
        expect(result.current.error).toBeNull();
    });

    it("keeps a missing-model status idle without an error", async () => {
        const { result } = renderHook(() => useVoiceInput());
        await waitFor(() => expect(mocks.init).toHaveBeenCalled());

        await act(async () => {
            mocks.listeners.get("voice:status")?.({
                available: true,
                ready: false,
                downloading: false,
                building: false,
                error: missingModelError,
            });
        });

        expect(result.current.state).toBe("idle");
        expect(result.current.error).toBeNull();
    });

    it("preserves other initialization errors", async () => {
        mocks.init.mockRejectedValue(new Error("boom"));

        const { result } = renderHook(() => useVoiceInput());

        await waitFor(() => expect(result.current.error).toBe("boom"));
    });
});
