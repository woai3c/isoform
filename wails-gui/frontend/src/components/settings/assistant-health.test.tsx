import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { AssistantHealthView } from "@/lib/wails-shim-app";
import { AssistantHealthLines } from "./assistant-health";

const mocks = vi.hoisted(() => ({
    getHealth: vi.fn(),
    retry: vi.fn(),
    eventsOn: vi.fn(),
    unsubscribe: vi.fn(),
    signIn: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetAssistantHealth: mocks.getHealth,
    RetryAssistantHealth: mocks.retry,
    OpenAssistantSignIn: mocks.signIn,
}));

vi.mock("../../../wailsjs/runtime/runtime", () => ({
    EventsOn: mocks.eventsOn,
}));

beforeEach(async () => {
    mocks.getHealth.mockReset();
    mocks.retry.mockReset();
    mocks.eventsOn.mockReset().mockReturnValue(mocks.unsubscribe);
    await i18n.changeLanguage("en");
});
afterEach(cleanup);

function line(assistant: string): HTMLElement {
    return screen.getByText(assistant === "codex" ? "Codex" : "Claude Code").closest("li")!;
}

describe("AssistantHealthLines", () => {
    it("signed out beside the name, with Sign in that opens the assistant's own sign-in (R75)", async () => {
        mocks.getHealth.mockResolvedValue([{ assistant: "claude-code", code: "signed_out", checking: false }]);
        mocks.signIn.mockResolvedValue(undefined);
        render(<AssistantHealthLines />);
        await screen.findByText("Signed out");
        fireEvent.click(within(line("claude-code")).getByRole("button", { name: "Sign in" }));
        expect(mocks.signIn).toHaveBeenCalledWith("claude-code");
    });


    it.each<[string, Partial<AssistantHealthView>, string, boolean]>([
        ["ready", {}, "Ready", false],
        ["check_failed", {}, "Paused after a failed check. Isoform answers until it passes.", true],
        ["repeated_errors", {}, "Paused after repeated errors. Isoform answers until it passes a check.", true],
        ["signed_out", {}, "Signed out", true],
        ["not_installed", {}, "Not installed on this computer.", true],
        ["unsupported_version", {}, "Needs an update to work with Isoform's tools.", true],
        ["tools_unverified", {}, "Cannot use Isoform's tools yet, so Isoform answers instead.", false],
        ["usage_limit", {}, "Usage limit reached. Isoform answers until it resets.", false],
        ["check_failed", { checking: true }, "Checking now", true],
    ])("words %s in plain language", async (code, extra, text, retry) => {
        mocks.getHealth.mockResolvedValue([{ assistant: "claude-code", code, checking: false, ...extra }]);
        render(<AssistantHealthLines />);
        expect(await screen.findByText(text)).toBeInTheDocument();
        expect(within(line("claude-code")).queryByRole("button", { name: "Try again" }) !== null).toBe(retry);
    });

    it("shows when a paused assistant is checked again", async () => {
        const later = new Date(Date.now() + 20 * 60_000);
        mocks.getHealth.mockResolvedValue([{ assistant: "claude-code", code: "check_failed", checking: false, retryAt: later.toISOString() }]);
        render(<AssistantHealthLines />);
        expect(await screen.findByText(/^Checks again at /)).toBeInTheDocument();
    });

    it("lists each assistant in order and checks one again on Try again", async () => {
        mocks.getHealth.mockResolvedValue([
            { assistant: "claude-code", code: "check_failed", checking: false },
            { assistant: "codex", code: "tools_unverified", checking: false },
        ]);
        mocks.retry.mockResolvedValue([
            { assistant: "claude-code", code: "check_failed", checking: true },
            { assistant: "codex", code: "tools_unverified", checking: false },
        ]);
        render(<AssistantHealthLines />);
        await screen.findByText("Claude Code");
        expect(screen.getAllByRole("listitem").map((item) => item.textContent)).toEqual([
            expect.stringContaining("Claude Code"),
            expect.stringContaining("Codex"),
        ]);
        fireEvent.click(within(line("claude-code")).getByRole("button", { name: "Try again" }));
        expect(mocks.retry).toHaveBeenCalledWith("claude-code");
        expect(await screen.findByText("Checking now")).toBeInTheDocument();
        expect(within(line("claude-code")).getByRole("button", { name: "Try again" })).toBeDisabled();
    });

    it("reads health again when the desktop says it changed", async () => {
        mocks.getHealth.mockResolvedValueOnce([{ assistant: "claude-code", code: "check_failed", checking: true }]);
        render(<AssistantHealthLines />);
        await screen.findByText("Checking now");
        expect(mocks.eventsOn).toHaveBeenCalledWith("assistant-health:changed", expect.any(Function));
        mocks.getHealth.mockResolvedValueOnce([{ assistant: "claude-code", code: "ready", checking: false }]);
        const changed = mocks.eventsOn.mock.calls[0][1] as () => void;
        await act(async () => changed());
        expect(await screen.findByText("Ready")).toBeInTheDocument();
    });

    it("follows account refreshes and order changes", async () => {
        mocks.getHealth.mockResolvedValueOnce([
            { assistant: "claude-code", code: "ready", checking: false },
            { assistant: "codex", code: "tools_unverified", checking: false },
        ]);
        const { unmount } = render(<AssistantHealthLines />);
        await screen.findByText("Claude Code");
        const configChanged = mocks.eventsOn.mock.calls.find(([name]) => name === "ai:config-changed")?.[1] as () => void;
        expect(configChanged).toBeDefined();
        mocks.getHealth.mockResolvedValueOnce([
            { assistant: "codex", code: "tools_unverified", checking: false },
            { assistant: "claude-code", code: "ready", checking: false },
        ]);
        await act(async () => configChanged());
        await waitFor(() =>
            expect(screen.getAllByRole("listitem").map((item) => item.textContent)).toEqual([
                expect.stringContaining("Codex"),
                expect.stringContaining("Claude Code"),
            ]),
        );
        mocks.unsubscribe.mockClear();
        unmount();
        expect(mocks.unsubscribe).toHaveBeenCalledTimes(2);
    });

    it("renders nothing when Isoform routes to no assistant", async () => {
        mocks.getHealth.mockResolvedValue([]);
        const { container } = render(<AssistantHealthLines />);
        await waitFor(() => expect(mocks.getHealth).toHaveBeenCalled());
        expect(container).toBeEmptyDOMElement();
    });

    it("says so calmly when Try again cannot reach the desktop", async () => {
        mocks.getHealth.mockResolvedValue([{ assistant: "claude-code", code: "signed_out", checking: false }]);
        mocks.retry.mockRejectedValue(new Error("Wails binding RetryAssistantHealth is not available"));
        render(<AssistantHealthLines />);
        fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Isoform could not check again. Try once more in a moment.");
    });

    it("speaks Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        mocks.getHealth.mockResolvedValue([{ assistant: "claude-code", code: "repeated_errors", checking: false }]);
        render(<AssistantHealthLines />);
        expect(await screen.findByText("多次出错，已暂停使用。通过检查前由 Isoform 来回答。")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "再试一次" })).toBeInTheDocument();
    });
});
