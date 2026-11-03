import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import enSettings from "@/i18n/locales/en/settings.json";
import zhSettings from "@/i18n/locales/zh-CN/settings.json";
import type { PluginInfo } from "@/lib/wails-shim-app";
import shimSource from "@/lib/wails-shim-app.ts?raw";
import pageSource from "./plugins-page.tsx?raw";
import { PluginsPage } from "./plugins-page";

const bindings = vi.hoisted(() => ({ list: vi.fn(), set: vi.fn(), error: vi.fn() }));

// These are component tests with stubbed bindings, not integration tests.
vi.mock("../../../../wailsjs/go/main/App", () => ({
    ListPlugins: bindings.list,
    SetBuiltinOn: bindings.set,
}));
vi.mock("sonner", () => ({ toast: { error: bindings.error } }));

function plugin(id: string, enabled: boolean, essential = false): PluginInfo {
    return {
        id,
        nameKey: `plugins.builtins.${id}.name`,
        descriptionKey: `plugins.builtins.${id}.description`,
        version: "1",
        kind: "builtin",
        source: "builtin",
        enabled,
        essential,
        lockedReasonKey: essential ? "plugins.essentialLocked" : "",
        readErrorKey: "",
    };
}

function rows(): PluginInfo[] {
    return [
        plugin("authority", true, true),
        plugin("cron-api", true, true),
        plugin("notification-pacing", true, true),
        { ...plugin("calendar-peek", true), kind: "source" },
        plugin("triage-shadow", true),
        plugin("shadow-confidence", true),
    ];
}

function leafKeys(node: unknown, prefix = ""): string[] {
    if (!node || typeof node !== "object") return [prefix];
    return Object.entries(node).flatMap(([key, value]) => leafKeys(value, prefix ? `${prefix}.${key}` : key)).sort();
}

describe("PluginsPage", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage("en");
        bindings.list.mockResolvedValue(rows());
    });

    it("renders built-in rows with translated metadata and no install actions", async () => {
        render(<PluginsPage />);
        expect(await screen.findByRole("switch", { name: "Web calendars" })).toBeChecked();
        const calendarRow = screen.getByRole("switch", { name: "Web calendars" }).closest(".settings-row")! as HTMLElement;
        expect(within(calendarRow).getByText("Reads web calendars that are not connected to this computer.")).toBeInTheDocument();
        for (const text of ["Version 1", "Source: Isoform", "Built in", "Source"]) {
            expect(within(calendarRow).getByText(text)).toBeInTheDocument();
        }
        expect(screen.queryByRole("button", { name: /Add plugin|Install|Update|Remove/i })).not.toBeInTheDocument();
    });

    it("calls the switch binding and displays its refreshed dependent states", async () => {
        bindings.set.mockResolvedValue(rows().map((row) =>
            ["triage-shadow", "shadow-confidence"].includes(row.id) ? { ...row, enabled: false } : row,
        ));
        render(<PluginsPage />);
        fireEvent.click(await screen.findByRole("switch", { name: "Learn by comparison" }));
        await waitFor(() => expect(bindings.set).toHaveBeenCalledWith("triage-shadow", false));
        expect(screen.getByRole("switch", { name: "Learn by comparison" })).not.toBeChecked();
        expect(screen.getByRole("switch", { name: "Adjust prediction confidence" })).not.toBeChecked();
    });

    it("locks the three essentials with an accessible explanation", async () => {
        render(<PluginsPage />);
        await screen.findByRole("switch", { name: "Command safety" });
        for (const name of ["Command safety", "Scheduler", "Notification pacing"]) {
            const control = screen.getByRole("switch", { name });
            expect(control).toBeChecked();
            expect(control).toBeDisabled();
            expect(control).toHaveAccessibleDescription("Needed for Isoform to run safely.");
            fireEvent.click(control);
        }
        expect(bindings.set).not.toHaveBeenCalled();
    });

    it("keeps saved values visible when a toggle fails", async () => {
        bindings.set.mockRejectedValue(new Error("backend detail"));
        render(<PluginsPage />);
        const control = await screen.findByRole("switch", { name: "Web calendars" });
        fireEvent.click(control);
        await waitFor(() => expect(bindings.error).toHaveBeenCalledWith(enSettings.plugins.toggleFailed));
        expect(control).toBeChecked();
        expect(control).toBeEnabled();
        expect(screen.queryByText("backend detail")).not.toBeInTheDocument();
    });

    it.each([
        ["text-channel", "Text messages", "enter a phone number before turning on text messages", "phoneNumber"],
        ["text-channel", "Text messages", "accept the text message terms before turning on text messages", "textConsent"],
        ["mobile-connection", "Phone connection", "enter the address of a relay before turning on the phone connection", "relay"],
        ["activity-monitor", "Activity monitoring", "screen observation is paused or switched off in Observation", "observation"],
        ["triage-shadow", "Learn by comparison", "plugin settings are unreadable: triage-shadow", "unreadable"],
    ] as const)("explains the %s refusal: %s", async (id, name, detail, key) => {
        bindings.list.mockResolvedValue([plugin(id, false)]);
        // Wails may reject with a string instead of an Error object.
        bindings.set.mockRejectedValue(`set plugin ${id}: ${detail}`);
        render(<PluginsPage />);
        fireEvent.click(await screen.findByRole("switch", { name }));
        await waitFor(() => expect(bindings.error).toHaveBeenCalledWith(enSettings.plugins.errors[key]));
        expect(screen.getByRole("switch", { name })).not.toBeChecked();
    });

    it("leaves unreadable rows unavailable while other switches work", async () => {
        const unreadable = { ...plugin("mobile-connection", true), readErrorKey: "plugins.readError" };
        bindings.list.mockResolvedValue([...rows(), unreadable]);
        bindings.set.mockResolvedValue([...rows().map((row) => row.id === "calendar-peek" ? { ...row, enabled: false } : row), unreadable]);
        render(<PluginsPage />);
        const unavailable = await screen.findByRole("switch", { name: "Phone connection" });
        expect(unavailable).toBeDisabled();
        expect(unavailable).toHaveAccessibleDescription(enSettings.plugins.readError);
        expect(screen.getByText(enSettings.plugins.stateUnavailable)).toBeInTheDocument();
        fireEvent.click(unavailable);
        expect(bindings.set).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole("switch", { name: "Web calendars" }));
        await waitFor(() => expect(bindings.set).toHaveBeenCalledWith("calendar-peek", false));
        expect(screen.getByRole("switch", { name: "Web calendars" })).not.toBeChecked();
        expect(unavailable).toBeDisabled();
    });

    it("refreshes changes already saved before a later dependency setter fails", async () => {
        bindings.set.mockRejectedValue(new Error("provider failed"));
        bindings.list.mockResolvedValueOnce(rows()).mockResolvedValueOnce(rows().map((row) =>
            row.id === "shadow-confidence" ? { ...row, enabled: false } : row,
        ));
        render(<PluginsPage />);
        fireEvent.click(await screen.findByRole("switch", { name: "Learn by comparison" }));
        await waitFor(() => expect(screen.getByRole("switch", { name: "Adjust prediction confidence" })).not.toBeChecked());
        expect(screen.getByRole("switch", { name: "Learn by comparison" })).toBeChecked();
    });

    it("prevents overlapping changes while one is saving", async () => {
        let finish!: (value: PluginInfo[]) => void;
        bindings.set.mockReturnValue(new Promise<PluginInfo[]>((resolve) => { finish = resolve; }));
        render(<PluginsPage />);
        fireEvent.click(await screen.findByRole("switch", { name: "Web calendars" }));
        expect(screen.getByRole("switch", { name: "Learn by comparison" })).toBeDisabled();
        await act(async () => finish(rows()));
        expect(screen.getByRole("switch", { name: "Learn by comparison" })).toBeEnabled();
    });

    it("shows a translated load error and retries", async () => {
        bindings.list.mockRejectedValueOnce(new Error("not ready"));
        render(<PluginsPage />);
        expect(await screen.findByRole("alert")).toHaveTextContent(enSettings.plugins.loadFailed);
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(await screen.findByRole("switch", { name: "Web calendars" })).toBeInTheDocument();
        expect(bindings.list).toHaveBeenCalledTimes(2);
    });

    it("renders names and locked reasons in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<PluginsPage />);
        expect(await screen.findByRole("switch", { name: "网页日历" })).toBeChecked();
        expect(screen.getByRole("switch", { name: "命令安全" })).toHaveAccessibleDescription("Isoform 安全运行所需。");
    });

    it("translates refusal guidance into Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        bindings.list.mockResolvedValue([plugin("text-channel", false)]);
        bindings.set.mockRejectedValue(new Error("enter a phone number before turning on text messages"));
        render(<PluginsPage />);
        fireEvent.click(await screen.findByRole("switch", { name: zhSettings.plugins.builtins["text-channel"].name }));
        await waitFor(() => expect(bindings.error).toHaveBeenCalledWith(zhSettings.plugins.errors.phoneNumber));
    });

    it("keeps English and Chinese plugin keys in parity", () => {
        expect(leafKeys(zhSettings.plugins)).toEqual(leafKeys(enSettings.plugins));
        expect(leafKeys(zhSettings.pages.plugins)).toEqual(leafKeys(enSettings.pages.plugins));
    });

    it("explicitly exports every App binding imported by the page from the shim", () => {
        const appImports = [...pageSource.matchAll(/import\s*\{([^}]+)\}\s*from\s*["'][^"']*wailsjs\/go\/main\/App["']/g)];
        const names = appImports.flatMap((entry) => entry[1].split(",").map((name) => name.trim()).filter(Boolean));
        expect(names).toEqual(["ListPlugins", "SetBuiltinOn"]);
        for (const name of names) {
            expect(shimSource).toMatch(new RegExp(`export const ${name} = appBinding<[^;]+\\("${name}"\\);`));
        }
    });

    it("retains the activity monitoring setter's explicit shim export", () => {
        expect(shimSource).toMatch(/export const SetActivityMonitorEnabled = appBinding<\[boolean\], void>\("SetActivityMonitorEnabled"\);/);
    });
});
