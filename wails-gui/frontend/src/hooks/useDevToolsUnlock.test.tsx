import { act, fireEvent, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
    Object.defineProperty(window.navigator, "platform", { value: "MacIntel", configurable: true });
});

const toastSuccess = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: toastSuccess } }));

import i18n from "@/i18n";
import { useDevToolsUnlock } from "./useDevToolsUnlock";

function pressShortcut(init: KeyboardEventInit = {}) {
    act(() => {
        fireEvent.keyDown(window, { code: "KeyD", key: "Î", metaKey: true, shiftKey: true, altKey: true, ...init });
    });
}

describe("useDevToolsUnlock", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        localStorage.clear();
        await i18n.changeLanguage("en");
    });

    it("turns Developer tools on and off again with the same shortcut", () => {
        const { result } = renderHook(() => useDevToolsUnlock());
        expect(result.current).toBe(false);

        pressShortcut();
        expect(result.current).toBe(true);
        expect(localStorage.getItem("yansu.devtools-unlocked")).toBe("1");
        expect(toastSuccess).toHaveBeenLastCalledWith("Developer tools on", {
            description: "Find them in Settings, below About.",
        });

        pressShortcut();
        expect(result.current).toBe(false);
        expect(localStorage.getItem("yansu.devtools-unlocked")).toBe("0");
        expect(toastSuccess).toHaveBeenLastCalledWith("Developer tools off", {
            description: "Press the same shortcut again to turn them back on.",
        });
    });

    it("starts from the build default and remembers an explicit choice over it", () => {
        const { result, rerender } = renderHook(({ dev }) => useDevToolsUnlock(dev), {
            initialProps: { dev: false },
        });
        expect(result.current).toBe(false);

        // Staging and dev builds have them on until the shortcut says otherwise.
        rerender({ dev: true });
        expect(result.current).toBe(true);
        pressShortcut();
        expect(result.current).toBe(false);
        rerender({ dev: true });
        expect(result.current).toBe(false);
    });

    it("ignores key repeat and other key combinations", () => {
        const { result } = renderHook(() => useDevToolsUnlock());
        pressShortcut({ repeat: true });
        pressShortcut({ altKey: false, key: "d" });
        expect(result.current).toBe(false);
        expect(toastSuccess).not.toHaveBeenCalled();
    });
});
