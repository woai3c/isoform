import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
    getSetup: vi.fn(),
    answer: vi.fn(),
    request: vi.fn(),
    openSettings: vi.fn(),
    ensureSelf: vi.fn(),
    profile: vi.fn(),
    selfRows: vi.fn(),
}));

// The Vite config routes wailsjs/go/main/App imports to the shim, so one
// mock of the shim covers both import paths.
vi.mock("@/lib/wails-shim-app", () => ({
    RequestPermission: mocks.request,
    OpenPermissionSettings: mocks.openSettings,
    GetMainChatSetup: mocks.getSetup,
    AnswerMainChatSetup: mocks.answer,
    EnsureSelfProfile: mocks.ensureSelf,
    GetUserProfile: mocks.profile,
    ListPrincipalsByKind: mocks.selfRows,
}));
vi.mock("../../wailsjs/runtime/runtime", () => ({ EventsOn: vi.fn(() => () => {}) }));

import { useMainChatSetup } from "./useMainChatSetup";
import { refreshUserProfile } from "./useUserProfile";

// The assistant was asked and answered in the chat; Screen Recording was
// already on (or turned on in onboarding) before the chat reached it.
const steps = [
    { key: "assistant", kind: "assistant", status: "granted", asked: true },
    { key: "screen_recording", kind: "permission", status: "granted", asked: false },
    { key: "microphone", kind: "permission", status: "pending", asked: true },
    { key: "contacts", kind: "permission", status: "pending", asked: true },
];

function stateAt(next: string) {
    return { steps: steps.map((s) => ({ ...s, status: s.key === next || steps.findIndex((x) => x.key === s.key) > steps.findIndex((x) => x.key === next) ? "pending" : "granted" })), next };
}

describe("useMainChatSetup", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mocks.getSetup.mockResolvedValue(stateAt("microphone"));
        mocks.answer.mockImplementation(async (step: string) => step === "microphone" ? stateAt("contacts") : { steps: [], next: "" });
        mocks.openSettings.mockResolvedValue(undefined);
        mocks.profile.mockResolvedValue(null);
        mocks.selfRows.mockResolvedValue([]);
    });

    it("shows the step Go names and never asks macOS on its own", async () => {
        const { result } = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(result.current.current?.key).toBe("microphone"));
        expect(mocks.getSetup).toHaveBeenCalledWith("en");
        // Screen Recording was never asked here, so it is not counted.
        expect(result.current.position).toBe(2);
        expect(result.current.total).toBe(3);
        window.dispatchEvent(new Event("focus"));
        await new Promise((r) => setTimeout(r, 350));
        expect(mocks.request).not.toHaveBeenCalled();
        expect(mocks.openSettings).not.toHaveBeenCalled();
    });

    it("cannot request Contacts from a stale setup card", async () => {
        mocks.getSetup.mockResolvedValue(stateAt("contacts"));
        const { result } = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(result.current.current?.key).toBe("contacts"));
        await act(async () => {
            await result.current.grant(result.current.current!);
            await result.current.openSettings(result.current.current!);
        });
        expect(mocks.request).not.toHaveBeenCalled();
        expect(mocks.openSettings).not.toHaveBeenCalled();
        expect(mocks.answer).not.toHaveBeenCalled();
    });

    it("records a grant only after the user's tap and macOS says yes", async () => {
        mocks.request.mockResolvedValue(true);
        const { result } = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(result.current.current?.key).toBe("microphone"));
        await act(async () => {
            await result.current.grant(result.current.current!);
        });
        expect(mocks.request).toHaveBeenCalledWith("microphone");
        expect(mocks.answer).toHaveBeenCalledWith("microphone", "granted");
        expect(result.current.current?.key).toBe("contacts");
        expect(result.current.waitingKey).toBeNull();
    });

    it("waits when macOS says yes but the live check does not agree yet", async () => {
        mocks.request.mockResolvedValue(true);
        mocks.answer.mockResolvedValue(stateAt("microphone"));
        const { result } = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(result.current.current?.key).toBe("microphone"));
        await act(async () => {
            await result.current.grant(result.current.current!);
        });
        expect(result.current.waitingKey).toBe("microphone");
        expect(mocks.openSettings).not.toHaveBeenCalled();
    });

    // The setup re-reads once when the window is shown (the shared change hook
    // reads on each show; the old focus throttle is gone). Window focus is a
    // "shown" signal in app-activity, so it also yields that one coalesced read.
    it("re-reads once each time the window is shown", async () => {
        const { result } = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(result.current.current?.key).toBe("microphone"));
        const calls = mocks.getSetup.mock.calls.length;
        // Hidden, then shown in a burst: one read for the show.
        Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
        document.dispatchEvent(new Event("visibilitychange"));
        Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
        document.dispatchEvent(new Event("visibilitychange"));
        document.dispatchEvent(new Event("visibilitychange"));
        await new Promise((r) => setTimeout(r, 350));
        expect(mocks.getSetup.mock.calls.length).toBe(calls + 1);
    });

    it("looks again before sending a slow prompt to System Settings", async () => {
        mocks.request.mockResolvedValue(false);
        mocks.getSetup.mockResolvedValueOnce(stateAt("microphone")).mockResolvedValueOnce(stateAt("contacts"));
        const { result } = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(result.current.current?.key).toBe("microphone"));
        await act(async () => {
            await result.current.grant(result.current.current!);
        });
        expect(mocks.openSettings).not.toHaveBeenCalled();
        expect(result.current.current?.key).toBe("contacts");
    });

    it("sends a refused permission to System Settings and waits there", async () => {
        mocks.request.mockResolvedValue(false);
        const { result } = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(result.current.current?.key).toBe("microphone"));
        await act(async () => {
            await result.current.grant(result.current.current!);
        });
        expect(mocks.openSettings).toHaveBeenCalledWith("microphone");
        expect(mocks.answer).not.toHaveBeenCalled();
        expect(result.current.waitingKey).toBe("microphone");
    });

    it("lets the user skip with Not now and resumes where Go left off", async () => {
        const first = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(first.result.current.current?.key).toBe("microphone"));
        await act(async () => {
            await first.result.current.notNow(first.result.current.current!);
        });
        expect(mocks.answer).toHaveBeenCalledWith("microphone", "not_now");
        first.unmount();
        // After a restart Go reports the step it stopped at.
        mocks.getSetup.mockResolvedValue(stateAt("contacts"));
        const second = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(second.result.current.current?.key).toBe("contacts"));
    });

    it("stays quiet when disabled", async () => {
        renderHook(() => useMainChatSetup(false));
        await new Promise((r) => setTimeout(r, 20));
        expect(mocks.getSetup).not.toHaveBeenCalled();
    });

    it("saves the name to the self profile before answering the name step", async () => {
        const nameStep = { key: "name", kind: "name", status: "pending", asked: true, suggestion: "Ada Lovelace" };
        mocks.getSetup.mockResolvedValue({ steps: [nameStep, ...steps], next: "name" });
        mocks.answer.mockResolvedValue({ steps: [{ ...nameStep, status: "granted" }, ...steps], next: "microphone" });
        const order: string[] = [];
        mocks.ensureSelf.mockImplementation(async () => {
            order.push("ensure");
            // Go now finds the name, so any later read has the step closed.
            mocks.getSetup.mockResolvedValue({ steps: [{ ...nameStep, status: "granted" }, ...steps], next: "microphone" });
            return {};
        });
        mocks.answer.mockImplementationOnce(async () => {
            order.push("answer");
            return { steps: [{ ...nameStep, status: "granted" }, ...steps], next: "microphone" };
        });
        mocks.profile.mockResolvedValue({ name: "Ada", display_name: "Ada" });
        const { result } = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(result.current.current?.key).toBe("name"));
        // Blank names are not saved.
        await act(async () => {
            await result.current.saveName(result.current.current!, "   ");
        });
        expect(mocks.ensureSelf).not.toHaveBeenCalled();
        await act(async () => {
            await result.current.saveName(result.current.current!, " Ada ");
        });
        expect(mocks.ensureSelf).toHaveBeenCalledWith("Ada", []);
        expect(mocks.answer).toHaveBeenCalledWith("name", "granted");
        expect(order).toEqual(["ensure", "answer"]);
        expect(result.current.current?.key).toBe("microphone");
        expect(result.current.busyKey).toBeNull();
    });

    it("does not write over a name stored since the card appeared, and looks again when the name changes", async () => {
        const nameStep = { key: "name", kind: "name", status: "pending", asked: true, suggestion: "Ada Lovelace" };
        mocks.getSetup.mockResolvedValue({ steps: [nameStep, ...steps], next: "name" });
        const { result } = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(result.current.current?.key).toBe("name"));

        // Renamed from the bottom-left menu: the profile changes, so setup is read again.
        const reads = mocks.getSetup.mock.calls.length;
        mocks.profile.mockResolvedValue({ name: "Ada L.", display_name: "Ada L." });
        await act(async () => {
            refreshUserProfile();
        });
        await waitFor(() => expect(mocks.getSetup.mock.calls.length).toBeGreaterThan(reads));

        // Save on the stale card asks instead of reverting the rename.
        mocks.selfRows.mockResolvedValue([{ id: "self", displayName: "Ada L." }]);
        mocks.answer.mockResolvedValue({ steps: [{ ...nameStep, status: "granted" }, ...steps], next: "microphone" });
        let outcome: unknown;
        await act(async () => {
            outcome = await result.current.saveName(nameStep as never, "Ada Lovelace");
        });
        expect(outcome).toEqual({ kind: "stored", name: "Ada L." });
        expect(mocks.ensureSelf).not.toHaveBeenCalled();
        expect(mocks.answer).not.toHaveBeenCalled();

        // Keep the stored name: nothing is written, the step closes.
        await act(async () => {
            outcome = await result.current.saveName(nameStep as never, "Ada L.");
        });
        expect(outcome).toEqual({ kind: "saved" });
        expect(mocks.ensureSelf).not.toHaveBeenCalled();
        expect(mocks.answer).toHaveBeenCalledWith("name", "granted");

        // Or replace it with the typed one.
        await act(async () => {
            outcome = await result.current.saveName(nameStep as never, "Ada Lovelace", true);
        });
        expect(outcome).toEqual({ kind: "saved" });
        expect(mocks.ensureSelf).toHaveBeenCalledWith("Ada Lovelace", []);
    });

    it("counts a stored name as saved even when closing the step fails, and ignores a second tap", async () => {
        const nameStep = { key: "name", kind: "name", status: "pending", asked: true };
        mocks.getSetup.mockResolvedValue({ steps: [nameStep, ...steps], next: "name" });
        mocks.ensureSelf.mockResolvedValue({});
        mocks.answer.mockRejectedValue(new Error("lost"));
        const { result } = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(result.current.current?.key).toBe("name"));
        const reads = mocks.getSetup.mock.calls.length;
        let first: unknown;
        let second: unknown;
        await act(async () => {
            const a = result.current.saveName(result.current.current!, "Ada");
            const b = result.current.saveName(result.current.current!, "Ada");
            [first, second] = await Promise.all([a, b]);
        });
        expect(first).toEqual({ kind: "saved" });
        expect(second).toEqual({ kind: "busy" });
        expect(mocks.ensureSelf).toHaveBeenCalledTimes(1);
        // The next read lets Go close the step.
        await waitFor(() => expect(mocks.getSetup.mock.calls.length).toBeGreaterThan(reads));
    });

    it("keeps the name step when the name cannot be saved", async () => {
        const nameStep = { key: "name", kind: "name", status: "pending", asked: true };
        mocks.getSetup.mockResolvedValue({ steps: [nameStep, ...steps], next: "name" });
        mocks.ensureSelf.mockRejectedValue(new Error("store closed"));
        const error = vi.spyOn(console, "error").mockImplementation(() => {});
        const { result } = renderHook(() => useMainChatSetup(true));
        await waitFor(() => expect(result.current.current?.key).toBe("name"));
        let outcome: unknown;
        await act(async () => {
            outcome = await result.current.saveName(result.current.current!, "Ada");
        });
        expect(outcome).toEqual({ kind: "failed" });
        expect(mocks.answer).not.toHaveBeenCalled();
        expect(result.current.current?.key).toBe("name");
        error.mockRestore();
    });

});
