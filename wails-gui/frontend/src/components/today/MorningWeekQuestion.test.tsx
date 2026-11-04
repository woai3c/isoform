import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@/i18n";
import { MorningWeekQuestion } from "./MorningWeekQuestion";

const mocks = vi.hoisted(() => ({ save: vi.fn(), change: vi.fn(), postChip: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({ SaveMorningBriefWeekIntent: mocks.save, ChangeMorningBriefWeekIntent: mocks.change, PostMorningBriefWeekAnswerFromChip: mocks.postChip }));

describe("MorningWeekQuestion", () => {
    beforeEach(() => { mocks.save.mockReset(); mocks.change.mockReset(); mocks.postChip.mockReset(); mocks.postChip.mockResolvedValue(undefined); });

    it("saves the recommendation only when the user confirms it", async () => {
        mocks.change.mockResolvedValue(undefined);
        render(<MorningWeekQuestion briefID="2026-10-26-morning" recommendation="Launch" />);
        expect(mocks.postChip).not.toHaveBeenCalled();
        fireEvent.click(screen.getByText("Still that"));
        await waitFor(() => expect(mocks.postChip).toHaveBeenCalledWith("2026-10-26-morning", "", "Launch"));
        expect(screen.getByText("You answered: Launch ·")).toBeTruthy();
        fireEvent.click(screen.getByRole("button", { name: "Change" }));
        fireEvent.change(screen.getByLabelText("What is this week about?"), { target: { value: "Writing" } });
        fireEvent.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(mocks.change).toHaveBeenCalledWith("2026-10-26-morning", "Launch", "Writing"));
        expect(screen.getByText("You answered: Writing ·")).toBeTruthy();
    });

    it("lets the user change the recommendation and retry a failed save", async () => {
        mocks.save.mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(undefined);
        render(<MorningWeekQuestion briefID="2026-10-26-morning" recommendation="Launch" />);
        fireEvent.click(screen.getByText("Something else"));
        fireEvent.change(screen.getByLabelText("What is this week about?"), { target: { value: "Writing" } });
        fireEvent.click(screen.getByText("Save"));
        expect(await screen.findByRole("alert")).toHaveTextContent("Didn't save. Try again.");
        fireEvent.click(screen.getByText("Save"));
        await waitFor(() => expect(mocks.save).toHaveBeenLastCalledWith("2026-10-26-morning", "Writing"));
        expect(screen.getByText("You answered: Writing ·")).toBeTruthy();
    });

    it("reopens the saved answer folded after a read", () => {
        render(<MorningWeekQuestion briefID="2026-10-26-morning" initialAnswer="Launch" />);
        expect(screen.getByText("You answered: Launch ·")).toBeTruthy();
        expect(screen.queryByRole("textbox")).toBeNull();
        expect(mocks.save).not.toHaveBeenCalled();
    });

    // Thursday polish C4: in Chat the week question has the style of every
    // Isoform question: body text, then "You answered: … · Change".
    it("uses the Chat question style in Chat: no rule, body-text question, one-space dot before Change", () => {
        render(<MorningWeekQuestion briefID="2026-10-26-morning" initialAnswer="Launch" variant="chat" />);
        const section = screen.getByTestId("morning-week-question");
        expect(section.className).not.toContain("border-t");
        const question = screen.getByText("What is this week about?");
        expect(question.className).not.toContain("font-semibold");
        expect(question.className).toContain("text-[14px]");
        const answered = section.querySelector("[data-week-answered]")!;
        expect(answered.textContent).toBe("You answered: Launch · Change");
        expect(answered.className).not.toMatch(/\bgap-/);
        fireEvent.click(screen.getByRole("button", { name: "Change" }));
        expect(screen.getByRole("textbox")).toBeTruthy();
    });

    it("enables Save for a typed answer and folds it after saving", async () => {
        mocks.save.mockResolvedValue(undefined);
        render(<MorningWeekQuestion briefID="2026-11-02-morning" />);
        const save = screen.getByRole("button", { name: "Save" });
        expect(save).toBeDisabled();
        fireEvent.change(screen.getByLabelText("What is this week about?"), { target: { value: "release isoform 1.0" } });
        expect(save).toBeEnabled();
        fireEvent.click(save);
        await waitFor(() => expect(mocks.save).toHaveBeenCalledWith("2026-11-02-morning", "release isoform 1.0"));
        expect(await screen.findByText("You answered: release isoform 1.0 ·")).toBeVisible();
        expect(screen.queryByRole("textbox")).toBeNull();
    });
});
