import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
    buildWelcomeContent,
    WelcomeSuggestions,
} from "./first-chat-welcome";

describe("first chat welcome", () => {
    it("describes the current agency actions without bespoke-app language", () => {
        const content = buildWelcomeContent({ slack: "Product" }, "Bo");

        expect(content).toContain("take over a bounded task");
        expect(content).toContain("recurring follow-through");
        expect(content).not.toMatch(/custom apps|build.*app/i);
    });

    it("offers handoff and recurring-work prompts", () => {
        const onFillComposer = vi.fn();
        render(<WelcomeSuggestions onFillComposer={onFillComposer} />);

        fireEvent.click(
            screen.getByRole("button", { name: "Take over this task..." }),
        );
        expect(onFillComposer).toHaveBeenCalledWith("Take over this task: ");
        expect(
            screen.queryByRole("button", { name: /build me an app/i }),
        ).not.toBeInTheDocument();
    });
});
