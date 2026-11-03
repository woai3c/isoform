import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { usePermissions } from "./usePermissions";

const mocks = vi.hoisted(() => ({
    getPermissions: vi.fn(),
    requestPermission: vi.fn(),
    openSettings: vi.fn(),
}));

vi.mock("../../wailsjs/go/main/App", () => ({
    GetPlatformPermissions: mocks.getPermissions,
    RequestPermission: mocks.requestPermission,
    OpenPermissionSettings: mocks.openSettings,
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: vi.fn(() => () => {}),
}));

vi.mock("sonner", () => ({
    toast: Object.assign(vi.fn(), { success: vi.fn(), warning: vi.fn(), error: vi.fn() }),
}));

const item = (key: string, granted = false) => ({
    key,
    label: key,
    description: "",
    granted,
    required: true,
    actionable: true,
    actionLabel: "Grant",
    toggleable: false,
    enabled: true,
});

describe("usePermissions", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mocks.getPermissions.mockResolvedValue({
            platform: "darwin",
            supported: true,
            items: [item("screen_recording"), item("accessibility")],
        });
        mocks.openSettings.mockResolvedValue(undefined);
    });

    it("leaves Contacts requests to the Who you know consent sheet", async () => {
        const { result } = renderHook(() => usePermissions());
        await waitFor(() => expect(result.current.permissions).not.toBeNull());
        await act(async () => { await result.current.requestPermission(item("contacts")); });
        expect(mocks.requestPermission).not.toHaveBeenCalled();
        expect(mocks.openSettings).not.toHaveBeenCalled();
    });

    it("leaves Messages Automation requests to text-message Settings", async () => {
        const { result } = renderHook(() => usePermissions());
        await waitFor(() => expect(result.current.permissions).not.toBeNull());
        await act(async () => { await result.current.requestPermission(item("automation_messages")); });
        expect(mocks.requestPermission).not.toHaveBeenCalled();
        expect(mocks.openSettings).not.toHaveBeenCalled();
    });

    // macOS shows one dialog per request; a second click while the first
    // prompt is open must not start another prompt.
    it("runs one permission request at a time", async () => {
        let answer: (granted: boolean) => void = () => {};
        mocks.requestPermission.mockImplementation(
            () => new Promise<boolean>((resolve) => { answer = resolve; }),
        );
        const { result } = renderHook(() => usePermissions());
        await waitFor(() => expect(result.current.permissions).not.toBeNull());

        let first: Promise<void> = Promise.resolve();
        act(() => {
            first = result.current.requestPermission(item("screen_recording"));
        });
        await waitFor(() => expect(result.current.requestingKey).toBe("screen_recording"));

        await act(async () => {
            await result.current.requestPermission(item("accessibility"));
        });
        expect(mocks.requestPermission).toHaveBeenCalledTimes(1);
        expect(mocks.requestPermission).toHaveBeenCalledWith("screen_recording");

        mocks.getPermissions.mockResolvedValue({
            platform: "darwin",
            supported: true,
            items: [item("screen_recording", true), item("accessibility")],
        });
        await act(async () => {
            answer(true);
            await first;
        });
        expect(result.current.requestingKey).toBeNull();

        // Once the first prompt is answered, the next request goes through.
        mocks.requestPermission.mockResolvedValue(true);
        await act(async () => {
            await result.current.requestPermission(item("accessibility"));
        });
        expect(mocks.requestPermission).toHaveBeenCalledTimes(2);
        expect(mocks.requestPermission).toHaveBeenLastCalledWith("accessibility");
    });
});
