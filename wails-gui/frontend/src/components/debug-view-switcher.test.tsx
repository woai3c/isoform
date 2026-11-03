import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import {
    DebugViewSwitcher,
    type DebugViewGroup,
} from "./debug-view-switcher";

type View = "tasks" | "predictions" | "patterns" | "quality";

const groups: readonly DebugViewGroup<View>[] = [
    {
        id: "shadow-work",
        label: "Observe and compare",
        views: [
            { id: "tasks", label: "Observed tasks" },
            { id: "predictions", label: "Predictions" },
        ],
    },
    {
        id: "learning",
        label: "Improve predictions",
        views: [
            { id: "patterns", label: "Work patterns" },
            { id: "quality", label: "Prediction quality" },
        ],
    },
];

function Harness() {
    const [view, setView] = useState<View>("tasks");
    return (
        <>
            <DebugViewSwitcher
                ariaLabel="Shadow mode"
                groups={groups}
                value={view}
                onValueChange={setView}
            />
            <output>{view}</output>
        </>
    );
}

describe("DebugViewSwitcher", () => {
    it("shows only the active group's views", () => {
        render(<Harness />);

        const groupTabs = screen.getByRole("tablist", {
            name: "Shadow mode groups",
        });
        const viewTabs = screen.getByRole("tablist", {
            name: "Observe and compare views",
        });
        expect(groupTabs).toHaveStyle({
            gridTemplateColumns: "repeat(2, minmax(0, 1fr))",
        });
        expect(viewTabs).toHaveStyle({
            gridTemplateColumns: "repeat(auto-fit, minmax(96px, 1fr))",
        });
        expect(groupTabs).toHaveClass("min-w-0");
        expect(viewTabs).toHaveClass("min-w-0");
        expect(
            screen.getByRole("tab", { name: "Observed tasks" }),
        ).toHaveAttribute("aria-selected", "true");
        expect(
            screen.getByRole("tab", { name: "Observed tasks" }),
        ).toHaveClass("min-w-0", "break-normal");
        expect(
            screen.queryByRole("tab", { name: "Prediction quality" }),
        ).not.toBeInTheDocument();
    });

    it("opens a group's first view before allowing a specific view", async () => {
        const user = userEvent.setup();
        render(<Harness />);

        await user.click(screen.getByRole("tab", { name: "Improve predictions" }));
        expect(screen.getByText("patterns")).toBeInTheDocument();
        expect(
            screen.getByRole("tablist", { name: "Improve predictions views" }),
        ).toBeInTheDocument();

        await user.click(
            screen.getByRole("tab", { name: "Prediction quality" }),
        );
        expect(screen.getByText("quality")).toBeInTheDocument();
        expect(
            screen.getByRole("tab", { name: "Prediction quality" }),
        ).toHaveAttribute("aria-selected", "true");
    });
});
