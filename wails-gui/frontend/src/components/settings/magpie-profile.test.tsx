import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { ANSWERING_PICKED_EVENT, MagpieProfileCard, modelName } from "./magpie-profile";

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
        fireEvent.click(screen.getByRole("option", { name: "Opus" }));
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

    it("is titled Models Isoform uses, with no magpie sentences", async () => {
        render(<MagpieProfileCard />);
        expect(await screen.findByRole("heading", { name: "Models Isoform uses" })).toBeInTheDocument();
        expect(screen.getByText("In order. Isoform uses the first one that has usage left.")).toBeInTheDocument();
        await screen.findByRole("button", { name: "Move Codex up" });
        expect(screen.queryByText(/magpie/)).not.toBeInTheDocument();
    });

    it("names models, not ids", () => {
        expect(modelName("claude/claude-sonnet-5-5")).toBe("Sonnet 5.5");
        expect(modelName("codex/gpt-5.5")).toBe("GPT-5.5");
        expect(modelName("claude/opus")).toBe("Opus");
    });

    it("says in copper when the models can't be read, and reads again", async () => {
        mocks.get.mockRejectedValueOnce(new Error("down"));
        render(<MagpieProfileCard />);
        const line = await screen.findByText("Couldn't read these models.");
        expect(line).toHaveClass("text-copper");
        fireEvent.click(within(line).getByRole("button", { name: "Try again" }));
        expect(await screen.findByRole("button", { name: "Move Codex up" })).toBeInTheDocument();
    });

    it("moves the assistant picked in Who answers to the top", async () => {
        render(<MagpieProfileCard />);
        await screen.findByRole("button", { name: "Move Codex up" });
        window.dispatchEvent(new CustomEvent(ANSWERING_PICKED_EVENT, { detail: "codex" }));
        await waitFor(() => expect(mocks.set).toHaveBeenLastCalledWith([initial.members[1], initial.members[0]]));
        mocks.set.mockClear();
        window.dispatchEvent(new CustomEvent(ANSWERING_PICKED_EVENT, { detail: "isoform" }));
        expect(mocks.set).not.toHaveBeenCalled();
    });

    it("uses Chinese labels", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<MagpieProfileCard />);
        expect(await screen.findByRole("heading", { name: "Isoform 使用的模型" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "将 Codex 上移" })).toBeInTheDocument();
    });
});
