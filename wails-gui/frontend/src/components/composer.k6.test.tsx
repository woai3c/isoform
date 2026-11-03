import type { ComponentProps } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { Composer } from "./composer";

vi.mock("@/hooks/useComposerSuggestions", () => ({
    useComposerSuggestions: () => ({
        suggestions: [],
        activeHandoffs: [],
        isLoading: false,
        isRefreshing: false,
        refresh: vi.fn(),
    }),
}));

function mount(overrides: Partial<ComponentProps<typeof Composer>> = {}) {
    const props: ComponentProps<typeof Composer> = {
        prompt: "",
        onPromptChange: vi.fn(),
        onSend: vi.fn(),
        onCancel: vi.fn(),
        onKeyDown: vi.fn(),
        executorGroups: [],
        streaming: false,
        ready: true,
        hasProject: true,
        images: [],
        onImagesChange: vi.fn(),
        files: [],
        onFilesChange: vi.fn(),
        executorType: "claude",
        queuedMessages: [],
        onRemoveQueuedMessage: vi.fn(),
        onEditQueuedMessage: vi.fn(),
        availableCommands: [],
        ...overrides,
    };
    return { props, view: render(<Composer {...props} />) };
}

describe("Chat K6 composer", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it.each([
        ["en", "Remove plan.pdf", "Remove diagram.png"],
        ["zh-CN", "移除 plan.pdf", "移除 diagram.png"],
    ])("names every attachment remove button and reaches it with Tab in %s", async (language, fileName, imageName) => {
        await i18n.changeLanguage(language);
        const user = userEvent.setup();
        const { props } = mount({
            files: [{
                id: "file-1",
                fileName: "plan.pdf",
                fileSize: 128,
                mediaType: "application/pdf",
                category: "document",
            }],
            images: [{ mediaType: "image/png", dataBase64: "aW1hZ2U=", fileName: "diagram.png" }],
        });
        const removeImage = screen.getByRole("button", { name: imageName });
        const removeFile = screen.getByRole("button", { name: fileName });
        expect(removeImage).toHaveClass("focus-visible:opacity-100", "focus-visible:ring-2");
        expect(removeFile).toHaveClass("focus-visible:opacity-100", "focus-visible:ring-2");

        await user.tab();
        await user.tab();
        expect(removeImage).toHaveFocus();
        await user.keyboard("{Enter}");
        expect(props.onImagesChange).toHaveBeenCalledWith([]);
        await user.tab();
        expect(removeFile).toHaveFocus();
        await user.keyboard("{Enter}");
        expect(props.onFilesChange).toHaveBeenCalledWith([]);
    });

    it("attaches a pasted PDF through the file attachment path", async () => {
        const { props } = mount();
        const pdf = new File(["%PDF-1.4"], "brief.pdf", { type: "application/pdf" });
        fireEvent.paste(screen.getByRole("textbox"), {
            clipboardData: {
                items: [{ kind: "file", type: pdf.type, getAsFile: () => pdf }],
            },
        });

        await waitFor(() => {
            expect(props.onFilesChange).toHaveBeenCalledWith([
                expect.objectContaining({
                    fileName: "brief.pdf",
                    fileSize: pdf.size,
                    mediaType: "application/pdf",
                    category: "document",
                    dataBase64: "JVBERi0xLjQ=",
                }),
            ]);
        });
        expect(props.onImagesChange).not.toHaveBeenCalled();
    });

    it.each([false, true])("shows no slash menu with zero commands when loading is %s", (loading) => {
        mount({ prompt: "/", slashCommandsLoading: loading });
        const textarea = screen.getByRole("textbox");
        fireEvent.focus(textarea);

        expect(textarea.parentElement?.childElementCount).toBe(1);
        expect(screen.queryByText("No slash commands available.")).not.toBeInTheDocument();
        expect(screen.queryByText("Loading commands...")).not.toBeInTheDocument();
    });

    it("keeps the no-match message for a real slash command list", () => {
        mount({ prompt: "/zzz", availableCommands: [{ name: "help" }] });
        const textarea = screen.getByRole("textbox");
        fireEvent.focus(textarea);

        expect(screen.getByText("No matching commands.")).toBeVisible();
        expect(textarea.parentElement?.childElementCount).toBe(2);
    });
});
