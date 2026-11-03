import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
    getThemePreference: vi.fn(),
    setThemePreference: vi.fn(),
}));

vi.mock("../../wailsjs/go/main/App", () => ({
    GetThemePreference: mocks.getThemePreference,
    SetThemePreference: mocks.setThemePreference,
}));

import { ThemeProvider, useTheme } from "./useTheme";

// A controllable prefers-color-scheme: dark media query.
function stubColorScheme(initiallyDark: boolean) {
    const listeners = new Set<(e: { matches: boolean }) => void>();
    const query = {
        matches: initiallyDark,
        media: "(prefers-color-scheme: dark)",
        addEventListener: (_: string, fn: (e: { matches: boolean }) => void) => listeners.add(fn),
        removeEventListener: (_: string, fn: (e: { matches: boolean }) => void) => listeners.delete(fn),
    };
    window.matchMedia = vi.fn().mockReturnValue(query);
    return (dark: boolean) => {
        query.matches = dark;
        act(() => {
            for (const fn of listeners) fn({ matches: dark });
        });
    };
}

const wrapper = ({ children }: { children: ReactNode }) => <ThemeProvider>{children}</ThemeProvider>;

describe("ThemeProvider", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        localStorage.clear();
        mocks.setThemePreference.mockResolvedValue(undefined);
        mocks.getThemePreference.mockResolvedValue("");
    });

    it("follows the system light and dark setting while set to Match system", async () => {
        const setSystemDark = stubColorScheme(false);
        const { result } = renderHook(() => useTheme(), { wrapper });

        act(() => result.current.setPreference("system"));
        expect(result.current.preference).toBe("system");
        expect(result.current.themeId).toBe("paper");
        expect(localStorage.getItem("yansu.theme")).toBe("system");

        setSystemDark(true);
        expect(result.current.themeId).toBe("onedark");
        expect(document.documentElement.getAttribute("data-theme")).toBe("onedark");
        // Go only ever receives a concrete theme, for the spotlight and toasts.
        await waitFor(() => expect(mocks.setThemePreference).toHaveBeenLastCalledWith("onedark"));
        expect(mocks.setThemePreference).not.toHaveBeenCalledWith("system");
    });

    it("keeps Match system when the Go side reports the theme it resolved to", async () => {
        stubColorScheme(true);
        localStorage.setItem("yansu.theme", "system");
        mocks.getThemePreference.mockResolvedValue("onedark");
        const { result } = renderHook(() => useTheme(), { wrapper });

        await waitFor(() => expect(mocks.getThemePreference).toHaveBeenCalled());
        await act(async () => {});
        expect(result.current.preference).toBe("system");
        expect(result.current.themeId).toBe("onedark");
    });

    it("still trusts the Go value over local storage for a fixed theme", async () => {
        stubColorScheme(false);
        localStorage.setItem("yansu.theme", "paper");
        mocks.getThemePreference.mockResolvedValue("onedark");
        const { result } = renderHook(() => useTheme(), { wrapper });

        await waitFor(() => expect(result.current.preference).toBe("onedark"));
    });
});
