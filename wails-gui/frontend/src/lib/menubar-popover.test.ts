import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it, vi } from "vitest";

const template = readFileSync(resolve(process.cwd(), "../menubar_window_assets/popover.html"), "utf8");

function page(overrides: Record<string, unknown> = {}) {
    // The 1.0 page's model (menubar_model.go menuBarView): words echo their keys.
    const words = Object.fromEntries([
        "observing", "paused", "off", "starting", "pause", "resume", "turnOn", "playing",
        "waiting", "seeAll", "empty", "failed", "make", "open", "openShortcut", "update", "restart",
        "settings", "footerQuit",
    ].map(key => [key, key]));
    const model = { language: "en", theme: "paper", words, status: "observing", state: "observing", paused: false,
        waiting: 5, brief: { kind: "ready", id: "brief", title: "brief", meta: "7:00 AM · 0:48", seconds: 48 },
        items: [{ id: "row", title: "title", detail: "detail" }], label: "Isoform", ...overrides };
    const document = new DOMParser().parseFromString(template.replace("/*__MODEL__*/null", JSON.stringify(model)), "text/html");
    const postMessage = vi.fn();
    const host = { webkit: { messageHandlers: { menubar: { postMessage } } } };
    // Run the production page script against an isolated unit-test document.
    // The native bridge is a unit boundary; no app or application API runs.
    const script = document.querySelector("script")!.textContent!;
    new Function("window", "document", "setInterval", script)(host, document, vi.fn());
    postMessage.mockClear();
    return { document, postMessage };
}

describe("menu bar embedded page", () => {
    it("opens Isoform with the footer's Command Shift I shortcut", () => {
        const { document, postMessage } = page();
        const event = new KeyboardEvent("keydown", { key: "I", metaKey: true, shiftKey: true, cancelable: true });
        document.dispatchEvent(event);
        expect(event.defaultPrevented).toBe(true);
        expect(postMessage).toHaveBeenCalledTimes(1);
        expect(postMessage).toHaveBeenCalledWith({ command: "open" });
    });
    it("leaves unmodified and other modified I keys alone", () => {
        const { document, postMessage } = page();
        for (const modifiers of [{}, { metaKey: true }, { metaKey: true, shiftKey: true, altKey: true }, { metaKey: true, shiftKey: true, ctrlKey: true }]) {
            document.dispatchEvent(new KeyboardEvent("keydown", { key: "i", ...modifiers, cancelable: true }));
        }
        expect(postMessage).not.toHaveBeenCalled();
    });
    it("dismisses the popover with Escape", () => {
        const { document, postMessage } = page();
        const event = new KeyboardEvent("keydown", { key: "Escape", cancelable: true });
        document.dispatchEvent(event);
        expect(event.defaultPrevented).toBe(true);
        expect(postMessage).toHaveBeenCalledTimes(1);
        expect(postMessage).toHaveBeenCalledWith({ command: "close" });
    });
    // 1.0 layout: the "Show menu" button is retired (right-click shows the
    // native menu); every remaining control still sends its command.
    it("keeps waiting navigation, playback, Make my brief now and the footer wired", () => {
        const { document, postMessage } = page();
        for (const selector of [".it", "#br", "#more", "#hb", "#make", "#open", "#settings", "#quit"]) {
            (document.querySelector(selector) as HTMLButtonElement).click();
        }
        // Pause changes the header at once, which reports its new height.
        expect(postMessage.mock.calls.map(([message]) => message.command).filter((command) => command !== "resize")).toEqual(
            ["waiting:row", "play:brief", "waiting", "pause15", "makeHere", "open", "settings", "quit"]);
        expect(document.querySelector("#menu")).toBeNull();
    });
    it("reads a brief with no recording instead of playing it", () => {
        const { document, postMessage } = page({ brief: { kind: "read", id: "evening", title: "Read", meta: "7:30 PM" } });
        (document.querySelector("#br") as HTMLButtonElement).click();
        expect(postMessage).toHaveBeenCalledWith({ command: "read:evening" });
    });
});
