import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { MagpieProfileCard } from "./magpie-profile";

const mocks = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn(), background: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({ GetMagpieProfile: mocks.get, SetMagpieProfile: mocks.set, SetBackgroundUsesChoice: mocks.background }));
vi.mock("../../../wailsjs/runtime/runtime", () => ({ EventsOn: () => () => undefined }));

const initial = {
    members: [{ model: "claude/sonnet", effort: "high" }, { model: "codex/astra", effort: "medium" }],
    models: ["claude/sonnet", "claude/opus", "codex/astra", "gemini/pro"],
    backgroundUsesChoice: false,
};
beforeEach(async () => {
    vi.clearAllMocks();
    mocks.get.mockResolvedValue(initial);
    mocks.set.mockImplementation(async (members) => ({ ...initial, members }));
    mocks.background.mockResolvedValue({});
    await i18n.changeLanguage("en");
});
afterEach(cleanup);

describe("Isoform magpie profile", () => {
    it("edits order, provider model, effort, membership and background choice", async () => {
        render(<MagpieProfileCard />);
        fireEvent.click(await screen.findByRole("button", { name: "Move Codex up" }));
        await waitFor(() => expect(mocks.set).toHaveBeenLastCalledWith([initial.members[1], initial.members[0]]));
        const model = await screen.findByRole("combobox", { name: "Claude Code model" });
        fireEvent.keyDown(model, { key: "ArrowDown" });
        fireEvent.click(screen.getByRole("option", { name: "claude/opus" }));
        await waitFor(() => expect(mocks.set).toHaveBeenLastCalledWith([initial.members[1], { model: "claude/opus", effort: "high" }]));
        const effort = screen.getByRole("radiogroup", { name: "Claude Code effort" });
        await waitFor(() => expect(within(effort).getByRole("radio", { name: "Max" })).toBeEnabled());
        fireEvent.click(within(effort).getByRole("radio", { name: "Max" }));
        await waitFor(() => expect(mocks.set).toHaveBeenLastCalledWith([initial.members[1], { model: "claude/opus", effort: "max" }]));
        const add = screen.getByRole("combobox", { name: "Add an assistant" });
        await waitFor(() => expect(add).toBeEnabled());
        fireEvent.keyDown(add, { key: "ArrowDown" });
        fireEvent.click(screen.getByRole("option", { name: "Gemini" }));
        await waitFor(() => expect(mocks.set).toHaveBeenLastCalledWith([initial.members[1], { model: "claude/opus", effort: "max" }, { model: "gemini/pro", effort: "high" }]));
        await waitFor(() => expect(screen.getByRole("button", { name: "Remove Gemini" })).toBeEnabled());
        fireEvent.click(screen.getByRole("button", { name: "Remove Gemini" }));
        await waitFor(() => expect(mocks.set).toHaveBeenLastCalledWith([initial.members[1], { model: "claude/opus", effort: "max" }]));
        const background = screen.getByRole("switch", { name: "Use my choice for background work too" });
        await waitFor(() => expect(background).toBeEnabled());
        fireEvent.click(background);
        await waitFor(() => expect(mocks.background).toHaveBeenCalledWith(true));
    });

    it("reports a rejected group update", async () => {
        mocks.set.mockRejectedValue(new Error("read only"));
        render(<MagpieProfileCard />);
        fireEvent.click(await screen.findByRole("button", { name: "Move Codex up" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Isoform could not save that choice");
    });

    it("uses Chinese labels", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<MagpieProfileCard />);
        expect(await screen.findByRole("heading", { name: "Isoform 模型配置" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "将 Codex 上移" })).toBeInTheDocument();
    });
});
