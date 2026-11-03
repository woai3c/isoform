import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import en from "@/i18n/locales/en/today.json";
import zh from "@/i18n/locales/zh-CN/today.json";
import { SuggestionCard } from "./SuggestionCard";

const suggestion = {
    available: true,
    id: "init-1",
    title: "Weekly status email",
    description: "Send the Monday status note to the team.",
    times: 4,
    days: 3,
    evidence: ["Drafted the status note in Mail", "Pasted last week's numbers"],
    schedule: "repeat" as const,
    scheduleDescription: "every Monday at 9",
    suggestedAt: "2026-10-12T09:00:00Z",
};

describe("SuggestionCard", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it("shows the pattern, the evidence and what accepting creates", () => {
        render(<SuggestionCard suggestion={suggestion} busy={false} failed={false} onResolve={vi.fn()} />);
        const card = screen.getByTestId("suggestion-card");
        expect(card).toHaveTextContent("Seen 4 times over 3 days");
        expect(card).toHaveTextContent("Weekly status email");
        expect(card).toHaveTextContent("Drafted the status note in Mail");
        expect(card).toHaveTextContent("Accepting schedules this in Tasks: every Monday at 9");
        expect(screen.getByRole("button", { name: "Schedule it" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Not now" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Dismiss" })).toBeInTheDocument();
    });

    it("offers Do it once for a pattern without a schedule", () => {
        render(
            <SuggestionCard
                suggestion={{ ...suggestion, schedule: "once", scheduleDescription: "" }}
                busy={false}
                failed={false}
                onResolve={vi.fn()}
            />,
        );
        expect(screen.getByRole("button", { name: "Do it once" })).toBeInTheDocument();
        expect(screen.getByTestId("suggestion-card")).toHaveTextContent(en.suggestion.once);
    });

    it("accepts through the decision callback and then says it is done", async () => {
        const onResolve = vi.fn().mockResolvedValue(true);
        render(<SuggestionCard suggestion={suggestion} busy={false} failed={false} onResolve={onResolve} />);
        fireEvent.click(screen.getByRole("button", { name: "Schedule it" }));
        await waitFor(() => expect(onResolve).toHaveBeenCalledWith("add_to_upcoming", undefined));
        expect(await screen.findByRole("status")).toHaveTextContent(en.suggestion.done);
    });

    it("asks why before dismissing and sends the reason", async () => {
        const onResolve = vi.fn().mockResolvedValue(true);
        render(<SuggestionCard suggestion={suggestion} busy={false} failed={false} onResolve={onResolve} />);
        fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
        const confirm = screen.getByRole("button", { name: "Dismiss with reason" });
        expect(confirm).toBeDisabled();
        fireEvent.change(screen.getByLabelText(en.suggestion.reasonLabel), { target: { value: "  Only for one project " } });
        expect(confirm).toBeEnabled();
        fireEvent.click(confirm);
        await waitFor(() => expect(onResolve).toHaveBeenCalledWith("dismiss", "Only for one project"));
    });

    it("can cancel the dismissal and shows a failure", () => {
        const { rerender } = render(<SuggestionCard suggestion={suggestion} busy={false} failed={false} onResolve={vi.fn()} />);
        fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
        fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
        expect(screen.getByRole("button", { name: "Schedule it" })).toBeInTheDocument();
        rerender(<SuggestionCard suggestion={suggestion} busy={false} failed onResolve={vi.fn()} />);
        expect(screen.getByRole("alert")).toHaveTextContent(en.suggestion.failed);
    });

    it("has the same suggestion keys in English and Chinese", () => {
        expect(Object.keys(zh.suggestion).sort()).toEqual(Object.keys(en.suggestion).sort());
        expect(zh.sections.suggestion).toBeTruthy();
    });
});
