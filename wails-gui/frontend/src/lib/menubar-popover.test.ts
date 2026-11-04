import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it, vi } from "vitest";

const template = readFileSync(resolve(process.cwd(), "../menubar_window_assets/popover.html"), "utf8");

function page(overrides: Record<string, unknown> = {}, wordOverrides: Record<string, string> = {}) {
    // The 1.0 page's model (menubar_model.go menuBarView): words echo their keys.
    const words = Object.fromEntries([
        "observing", "paused", "off", "starting", "pause", "resume", "turnOn", "playing",
        "waiting", "seeAll", "empty", "failed", "make", "open", "openShortcut", "update", "restart",
        "settings", "footerQuit", "onThisMac",
    ].map(key => [key, key]));
    Object.assign(words, wordOverrides);
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
    // shell-review v1.0.0 B5: what's saved shows at once; until Isoform
    // knows, the section isn't there. Never placeholders.
    it("shows saved rows at once and leaves Waiting on you out until it knows", () => {
        const saved = page();
        expect(saved.document.querySelector("#wsec")!.hasAttribute("hidden")).toBe(false);
        expect(saved.document.querySelector(".it")!.textContent).toContain("title");
        for (const state of [{ loading: true }, { withdrawn: true }, { waitingPending: true }, { error: "failed", items: [] }]) {
            const { document } = page(state);
            expect(document.querySelector("#wsec")!.hasAttribute("hidden")).toBe(true);
            expect(document.querySelectorAll(".ph, .phb")).toHaveLength(0);
        }
    });
    // Evening walk B7: Waiting on you is what Tasks › Needs you lists, with no
    // count; with nothing there, the section isn't shown.
    it("shows Waiting on you with no count, and leaves it out when Needs you is empty", () => {
        const listed = page({ waiting: 1 });
        expect(listed.document.querySelector("#wsec")!.hasAttribute("hidden")).toBe(false);
        expect(listed.document.querySelector("#count")).toBeNull();
        expect(listed.document.querySelector("#wsec .sh")!.textContent).toBe("waiting");
        const none = page({ waiting: 0, items: [] });
        expect(none.document.querySelector("#wsec")!.hasAttribute("hidden")).toBe(true);
    });

    // Shell review v1.0.2 B7: no count in any state, and one item format whose
    // first line wraps instead of being cut.
    it("shows no count in the paused popover, and wraps the item's first line", () => {
        const paused = page({ waiting: 2, paused: true, status: "paused", state: "paused" });
        expect(paused.document.querySelector("#wsec .sh")!.textContent).toBe("waiting");
        const css = template.slice(template.indexOf("<style"), template.indexOf("</style>"));
        const title = css.match(/\.it b \{[^}]*\}/)![0];
        expect(title).not.toMatch(/nowrap|ellipsis|height:56px/);
        expect(css).not.toMatch(/\.it \{ height:/);
    });

    it("shows no brief placeholder before the first read", () => {
        const { document } = page({ loading: true });
        expect(document.querySelector("#br")!.hasAttribute("hidden")).toBe(true);
        expect(document.querySelectorAll(".ph, .phb")).toHaveLength(0);
    });
    // B4: while today's brief is still being tried, the row is the tries line.
    it("shows the tries line as a row that does nothing", () => {
        const { document, postMessage } = page({ brief: { kind: "trying", title: "Tried 13 times without finishing. Isoform tries again at 4:35 PM." } });
        const row = document.querySelector("#br") as HTMLButtonElement;
        expect(row.disabled).toBe(true);
        expect(document.querySelector("#briefTitle")!.textContent).toBe("Tried 13 times without finishing. Isoform tries again at 4:35 PM.");
        row.click();
        expect(postMessage).not.toHaveBeenCalledWith(expect.objectContaining({ command: expect.stringMatching(/^(play|read):/) }));
        expect((document.querySelector("#make") as HTMLButtonElement).hidden).toBe(false);
    });
    // B6: the footer link names the sheet it opens.
    it("labels the footer link with the sheet's name", () => {
        const { document } = page();
        expect(document.querySelector("#sees")!.textContent).toBe("onThisMac");
    });
    it("says until when Pause 15 min pauses, in English and Chinese", () => {
        vi.useFakeTimers();
        vi.setSystemTime(new Date(2026, 10, 5, 19, 0));
        try {
            for (const [language, want] of [["en", "Paused until 7:15 PM"], ["zh-CN", "已暂停至 19:15"]]) {
                const { document, postMessage } = page({ language }, { pausedUntil: language === "en" ? "Paused until {time}" : "已暂停至 {time}" });
                (document.getElementById("hb") as HTMLButtonElement).click();
                expect(postMessage).toHaveBeenCalledWith({ command: "pause15" });
                expect(document.getElementById("state")!.textContent).toBe(want);
                expect(document.getElementById("hb")!.textContent).toBe("resume");
            }
        } finally {
            vi.useRealTimers();
        }
    });
});
