import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { AddProjectURL } from "./add-project-url";

const mocks = vi.hoisted(() => ({ settings: vi.fn() }));
vi.mock("../../wailsjs/go/main/App", () => ({ GetWorktreeSettings: mocks.settings }));

describe("AddProjectURL flavor folder", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage("en");
    });

    it("waits for the resolved flavor folder before allowing a clone", async () => {
        let finish!: (settings: { repoRoot: string }) => void;
        mocks.settings.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
        const onCloneURL = vi.fn();
        const { container } = render(<AddProjectURL onCloneURL={onCloneURL} />);
        const input = screen.getByRole("textbox");
        fireEvent.change(input, { target: { value: "https://github.com/example/repo.git" } });
        expect(screen.getByRole("button", { name: "Clone" })).toBeDisabled();
        fireEvent.keyDown(input, { key: "Enter" });
        expect(onCloneURL).not.toHaveBeenCalled();
        expect(container).not.toHaveTextContent(".yansu-agent");

        await act(async () => finish({ repoRoot: "/tmp/isoform-override/repos" }));
        expect(screen.getByText("/tmp/isoform-override/repos/repo")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Clone" }));
        expect(onCloneURL).toHaveBeenCalledWith("https://github.com/example/repo.git");
    });

    it("does not invent a default folder when settings cannot be loaded", async () => {
        mocks.settings.mockRejectedValue(new Error("settings unavailable"));
        render(<AddProjectURL onCloneURL={vi.fn()} />);
        fireEvent.change(screen.getByRole("textbox"), { target: { value: "https://github.com/example/repo.git" } });

        expect(await screen.findByRole("alert")).toHaveTextContent("Could not load the repository folder.");
        expect(screen.getByRole("button", { name: "Clone" })).toBeDisabled();
    });
});
