import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { OtherAIAppsPage } from "@/components/settings/pages/other-ai-apps-page";

const mocks = vi.hoisted(() => ({
    openURL: vi.fn(),
}));

vi.mock("../../wailsjs/go/main/App", () => ({
    OpenExternalURL: mocks.openURL,
}));

describe("Other AI apps", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage("en");
    });

    it("is one card with the two actions, and the rest under the shared Advanced", () => {
        render(<OtherAIAppsPage />);

        expect(screen.getByText("Add Isoform to another assistant")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Copy install prompt" })).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "View on GitHub" }));
        expect(mocks.openURL).toHaveBeenCalledWith("https://github.com/Isoform/yansu-skill");
        expect(screen.queryByRole("button", { name: "Learn more" })).not.toBeInTheDocument();

        const advanced = screen.getByRole("button", { name: /Advanced/ });
        expect(advanced).toHaveAttribute("aria-expanded", "false");
        expect(screen.queryByText("What other assistants can recall")).not.toBeInTheDocument();

        fireEvent.click(advanced);
        expect(screen.getByText("What other assistants can recall")).toBeInTheDocument();
        expect(screen.getByText("Examples")).toBeInTheDocument();
        expect(screen.getByText("In any assistant")).toBeInTheDocument();
        expect(screen.getByText(/raw\.githubusercontent\.com\/Isoform\/yansu-skill/)).toBeInTheDocument();
        // One word for the other app throughout: assistant.
        expect(document.body.textContent).not.toMatch(/\bagent\b/i);
        // Remembered per page, like every other Advanced section.
        expect(window.localStorage.getItem("yansu.settings.advanced.use-yansu-anywhere")).toBe("1");
    });

    it("renders in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        window.localStorage.setItem("yansu.settings.advanced.use-yansu-anywhere", "1");
        render(<OtherAIAppsPage />);

        expect(screen.getByRole("button", { name: "复制安装提示词" })).toBeInTheDocument();
        expect(screen.getByText("其他助手能调用哪些内容")).toBeInTheDocument();
        expect(screen.getByText("在任意助手中")).toBeInTheDocument();
        expect(document.body.textContent).not.toMatch(/智能体|召回/);
    });
});
