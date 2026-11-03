import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import en from "@/i18n/locales/en/chat.json";
import zh from "@/i18n/locales/zh-CN/chat.json";

const mocks = vi.hoisted(() => ({
    files: vi.fn(),
    shells: vi.fn(),
    listeners: new Map<string, (payload: unknown) => void>(),
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, cb: (payload: unknown) => void) => {
        mocks.listeners.set(name, cb);
        return () => mocks.listeners.delete(name);
    },
}));
// The generated App module resolves to the shim in tests.
vi.mock("@/lib/wails-shim-app", () => ({ GetBackgroundShells: mocks.shells, GetWorkspaceChangedFiles: mocks.files }));

import { ProjectThreadStatus, commandStateFromDone } from "@/components/project-thread-status";

const emit = (name: string, payload: unknown) => act(() => mocks.listeners.get(name)?.(payload));

describe("ProjectThreadStatus (K7)", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
        mocks.files.mockReset().mockResolvedValue([
            { path: "src/app.ts", status: "M", staged: false },
            { path: "README.md", status: "A", staged: false },
        ]);
        mocks.shells.mockReset().mockResolvedValue([
            { shell_id: "sh-1", tag: "chat-1", command: "pnpm test", started_at: Date.now() - 5000, running: true },
        ]);
        mocks.listeners.clear();
    });
    afterEach(cleanup);

    it("shows the project's folder, changed files as chips and its running step", async () => {
        render(<ProjectThreadStatus sessionTag="chat-1" folder="/Users/example/isoform" branch="main" />);
        expect(screen.getByText("Folder /Users/example/isoform")).toBeInTheDocument();
        const chips = await screen.findByRole("list", { name: en.projectThread.changedFiles });
        expect(within(chips).getByText("app.ts")).toBeInTheDocument();
        expect(within(chips).getByText("README.md")).toBeInTheDocument();
        expect(mocks.files).toHaveBeenCalledWith("/Users/example/isoform", "main");
        const step = await screen.findByText(/Running · pnpm test · \d+s/);
        expect(step).toHaveAttribute("data-command-state", "running");
    });

    it.each([
        [{ exit: 0, status: "completed" }, "finished", "Finished · go test"],
        [{ exit: 2, status: "failed" }, "failed", "Exited with 2 · go test"],
        [{ exit: -1, status: "failed" }, "stopped", "Stopped · go test"],
    ])("reads a step that ended (%o) as %s", async (done, state, text) => {
        mocks.shells.mockResolvedValue([]);
        render(<ProjectThreadStatus sessionTag="chat-1" folder="/p" />);
        await waitFor(() => expect(mocks.listeners.has("bg:shell:done")).toBe(true));
        emit("bg:shell:done", { tag: "chat-1", shell_id: "sh-2", command: "go test", ...done });
        const line = await screen.findByText(text);
        expect(line).toHaveAttribute("data-command-state", state);
        expect(commandStateFromDone({ ...done })).toBe(state);
    });

    it("ignores another chat's steps and says so when the steps can't be read, in zh-CN", async () => {
        await i18n.changeLanguage("zh-CN");
        mocks.shells.mockRejectedValue(new Error("down"));
        render(<ProjectThreadStatus sessionTag="chat-1" folder="/p" />);
        expect(await screen.findByRole("status")).toHaveTextContent(zh.projectThread.command.unreadable);
        emit("bg:shell:done", { tag: "chat-9", shell_id: "sh-3", command: "ls", exit: 0 });
        expect(screen.queryByText(/ls/)).toBeNull();
        expect(screen.getByText("文件夹 /p")).toBeInTheDocument();
    });

    it("renders nothing without a folder or steps", async () => {
        mocks.shells.mockResolvedValue([]);
        const { container } = render(<ProjectThreadStatus sessionTag="" />);
        await act(async () => {});
        expect(container).toBeEmptyDOMElement();
    });
});
