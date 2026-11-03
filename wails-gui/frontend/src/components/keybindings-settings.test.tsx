import { useState } from "react";
import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

// The shortcut list is platform-aware (Pause observing exists on macOS and
// Windows only), and lib/platform reads navigator.platform at import time.
vi.hoisted(() => {
    Object.defineProperty(window.navigator, "platform", { value: "MacIntel", configurable: true });
});

const mocks = vi.hoisted(() => ({
    getQuickChatHotkey: vi.fn(),
    setQuickChatHotkey: vi.fn(),
    setQuickChatHotkeyRecording: vi.fn(),
    toastSuccess: vi.fn(),
    toastError: vi.fn(),
}));

vi.mock("../../wailsjs/go/main/App", () => ({
    GetQuickChatHotkey: mocks.getQuickChatHotkey,
    SetQuickChatHotkey: mocks.setQuickChatHotkey,
    SetQuickChatHotkeyRecording: mocks.setQuickChatHotkeyRecording,
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: vi.fn(() => () => {}),
}));

vi.mock("sonner", () => ({
    toast: { success: mocks.toastSuccess, error: mocks.toastError, info: vi.fn() },
}));

import i18n from "@/i18n";
import { useKeybindings } from "@/hooks/useKeybindings";
import { KeybindingsSettings, QuickChatShortcutCard } from "./keybindings-settings";

function Harness() {
    const keybindings = useKeybindings();
    const [vimMode, setVimMode] = useState(false);
    return (
        <>
            <QuickChatShortcutCard keybindings={keybindings} />
            <KeybindingsSettings keybindings={keybindings} vimMode={vimMode} onVimModeChange={setVimMode} />
        </>
    );
}

// Recording listeners attach 50 ms after the click so the click itself is
// not captured.
async function startRecording(name: string) {
    fireEvent.click(screen.getByRole("button", { name }));
    await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 60));
    });
}

describe("Keyboard shortcuts", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        localStorage.clear();
        Element.prototype.scrollIntoView = vi.fn();
        await i18n.changeLanguage("en");
        mocks.getQuickChatHotkey.mockResolvedValue(null);
        mocks.setQuickChatHotkey.mockResolvedValue(undefined);
        mocks.setQuickChatHotkeyRecording.mockResolvedValue(undefined);
    });

    it.each([
        ["en", "Pause observing for one hour", "Pause observing for one hour"],
        ["zh-CN", "暂停观察一小时", "暂停观察一小时"],
    ])("names the global pause and translates action metadata in %s", async (language, label, description) => {
        await i18n.changeLanguage(language);
        const { result } = renderHook(() => useKeybindings());
        expect(result.current.actions.find((action) => action.id === "pause-observing")?.label).toBe(label);
        expect(result.current.actions.find((action) => action.id === "pause-observing")?.description).toContain(description);
        expect(result.current.actions.find((action) => action.id === "keyboard-shortcuts")?.label).toBe(i18n.t("settings:keybindings.commands.keyboard-shortcuts.label"));
        expect(result.current.actions.find((action) => action.id === "keyboard-shortcuts")?.description).toBe(i18n.t("settings:keybindings.commands.keyboard-shortcuts.description"));
    });

    it("opens keyboard shortcuts with Command slash", () => {
        const { result } = renderHook(() => useKeybindings());
        const event = new KeyboardEvent("keydown", { key: "/", metaKey: true });
        expect(result.current.findMatchingAction(event)).toBe("keyboard-shortcuts");
    });

    it("lists the in-app shortcuts and the read-only Pause observing shortcut, not Quick Chat or the developer replay", () => {
        render(<Harness />);

        expect(screen.getByRole("button", { name: "Keyboard shortcuts: ⌘/" })).toBeInTheDocument();
        for (const label of ["Search and commands", "Show or hide the sidebar", "Open settings", "New chat", "Send message"]) {
            expect(screen.getByText(label)).toBeInTheDocument();
        }
        expect(screen.getByRole("button", { name: "Pause observing for one hour: ⌃⌥⌘E" })).toBeDisabled();
        expect(screen.queryByText("Replay onboarding")).not.toBeInTheDocument();
        // Quick Chat lives on its own card, once.
        expect(screen.getAllByText("Quick Chat shortcut")).toHaveLength(1);
        expect(screen.queryByText("Quick Chat")).not.toBeInTheDocument();
        // Nothing customised yet.
        expect(screen.queryByRole("button", { name: "Reset all shortcuts" })).not.toBeInTheDocument();
    });

    it("records a new shortcut and refuses the keys Pause observing already uses", async () => {
        render(<Harness />);

        await startRecording("Search and commands: ⌘K");
        fireEvent.keyDown(window, { key: "j", metaKey: true });
        expect(screen.getByRole("button", { name: "Search and commands: ⌘J" })).toBeInTheDocument();
        expect(mocks.toastSuccess).toHaveBeenCalledWith("Shortcut updated");

        await startRecording("New chat: ⌘T");
        fireEvent.keyDown(window, { key: "e", metaKey: true, ctrlKey: true, altKey: true });
        expect(mocks.toastError).toHaveBeenCalledWith(
            'Conflicts with "Pause observing for one hour". Choose a different shortcut.',
        );
        expect(screen.getByText("Press a shortcut...")).toBeInTheDocument();
    });

    it("asks before resetting, and resets only the shortcuts on this card", async () => {
        render(<Harness />);

        await startRecording("Search and commands: ⌘K");
        fireEvent.keyDown(window, { key: "j", metaKey: true });

        fireEvent.click(screen.getByRole("button", { name: "Reset all shortcuts" }));
        expect(screen.getByText("Reset all shortcuts?")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Reset shortcuts" }));

        expect(screen.getByRole("button", { name: "Search and commands: ⌘K" })).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Reset all shortcuts" })).not.toBeInTheDocument();
        expect(mocks.setQuickChatHotkey).not.toHaveBeenCalled();
    });

    it("explains that the Vim keys need Vim-style navigation turned on", () => {
        render(<Harness />);

        fireEvent.click(screen.getByRole("button", { name: "Show the Vim keys" }));
        expect(screen.getByText("Turn on Vim-style navigation to use these keys.")).toBeInTheDocument();
        expect(screen.getByText("Scroll down")).toBeInTheDocument();
        expect(screen.getByText("Insert mode")).toBeInTheDocument();
        expect(screen.getByText("Move the cursor to the chat box")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("switch", { name: "Vim-style navigation" }));
        expect(screen.queryByText("Turn on Vim-style navigation to use these keys.")).not.toBeInTheDocument();
    });

    it.each(["en", "zh-CN"])("offers exactly four Quick Chat presets in human words in %s", async (language) => {
        await i18n.changeLanguage(language);
        render(<Harness />);
        const select = screen.getByRole("combobox", { name: i18n.t("settings:keybindings.quickChat.title") });
        await waitFor(() => expect(select).toBeEnabled());
        expect(select).toHaveTextContent(i18n.t("settings:keybindings.quickChat.presets.optionTwice"));
        fireEvent.keyDown(select, { key: "ArrowDown" });
        expect(screen.getAllByRole("option").map((option) => option.textContent)).toEqual([
            i18n.t("settings:keybindings.quickChat.presets.optionTwice"),
            i18n.t("settings:keybindings.quickChat.presets.controlSpace"),
            i18n.t("settings:keybindings.quickChat.presets.commandShiftSpace"),
            i18n.t("settings:keybindings.quickChat.presets.off"),
        ]);
        expect(select.textContent).not.toMatch(/[⌥⌘⌃⇧]/);
        expect(mocks.setQuickChatHotkeyRecording).not.toHaveBeenCalled();
    });

    it.each([
        ["Control and Space", { meta: false, ctrl: true, alt: false, shift: false, key: " " }],
        ["Command, Shift and Space", { meta: true, ctrl: false, alt: false, shift: true, key: " " }],
        ["Off", { meta: false, ctrl: false, alt: false, shift: false, key: "off" }],
        ["Tap Option twice", { meta: false, ctrl: false, alt: false, shift: false, key: "opt-opt" }],
    ])("saves %s using the existing binding model", async (name, combo) => {
        mocks.getQuickChatHotkey.mockResolvedValue({ meta: true, ctrl: false, alt: false, shift: false, key: "j" });
        render(<Harness />);
        const select = screen.getByRole("combobox", { name: "Quick Chat shortcut" });
        await waitFor(() => expect(select).toBeEnabled());
        expect(select).toHaveTextContent("Previously saved shortcut");
        expect(mocks.setQuickChatHotkey).not.toHaveBeenCalled();
        fireEvent.keyDown(select, { key: "ArrowDown" });
        fireEvent.click(screen.getByRole("option", { name }));
        await waitFor(() => expect(mocks.setQuickChatHotkey).toHaveBeenCalledWith(combo));
        await waitFor(() => expect(select).toHaveTextContent(name));
        expect(mocks.setQuickChatHotkeyRecording).not.toHaveBeenCalled();
        expect(JSON.parse(localStorage.getItem("yansu.keybindings") ?? "{}")["quick-chat-global"]).toEqual(combo);
    });

    it("keeps the previous selection if applying a preset fails", async () => {
        mocks.setQuickChatHotkey.mockRejectedValue(new Error("unavailable"));
        render(<Harness />);
        const select = screen.getByRole("combobox", { name: "Quick Chat shortcut" });
        await waitFor(() => expect(select).toBeEnabled());
        fireEvent.keyDown(select, { key: "ArrowDown" });
        fireEvent.click(screen.getByRole("option", { name: "Off" }));
        await waitFor(() => expect(mocks.toastError).toHaveBeenCalled());
        expect(select).toHaveTextContent("Tap Option twice");
        expect(JSON.parse(localStorage.getItem("yansu.keybindings") ?? "{}")).toEqual({});
    });
});
