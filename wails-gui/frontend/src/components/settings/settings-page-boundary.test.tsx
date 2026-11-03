import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import "@/i18n";
import { SettingsPageBoundary } from "./settings-page-boundary";

let shouldThrow = true;
function Flaky() {
    if (shouldThrow) throw new Error("boom");
    return <p>page content</p>;
}

describe("SettingsPageBoundary", () => {
    it("shows a recoverable message instead of a blank window when a page throws", () => {
        const spy = vi.spyOn(console, "error").mockImplementation(() => {});
        shouldThrow = true;
        render(
            <SettingsPageBoundary>
                <Flaky />
            </SettingsPageBoundary>,
        );
        expect(screen.getByText("This page could not be shown")).toBeInTheDocument();

        shouldThrow = false;
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(screen.getByText("page content")).toBeInTheDocument();
        spy.mockRestore();
    });
});
