import type { ComponentProps } from "react";
import { render, screen, within } from "@testing-library/react";
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
        prompt: "What changed?",
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
        executorType: "codex",
        queuedMessages: [],
        onRemoveQueuedMessage: vi.fn(),
        onEditQueuedMessage: vi.fn(),
        availableCommands: [],
        voiceState: "idle",
        onVoiceStart: vi.fn(),
        onVoiceStop: vi.fn(),
        ...overrides,
    };
    return { props, view: render(<Composer {...props} />) };
}

describe("Chat K9 the box is for asking", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it.each(["en", "zh-CN"])("has the field, attach, voice and Send only, even for a Codex chat (%s)", async (language) => {
        await i18n.changeLanguage(language);
        const { view } = mount();
        const box = view.container;
        expect(within(box).getByRole("textbox")).toBeInTheDocument();
        expect(within(box).getByPlaceholderText(i18n.t("composer.placeholder.default", { ns: "app" }))).toBeInTheDocument();
        expect(within(box).getByRole("button", { name: i18n.t("composer.voice.input", { ns: "app" }) })).toBeInTheDocument();
        expect(within(box).getByRole("button", { name: i18n.t("composer.actions.sendMessage", { ns: "app" }) })).toBeInTheDocument();
        expect(within(box).queryByRole("combobox")).toBeNull();
        expect(within(box).queryByRole("switch")).toBeNull();
        const names = within(box).getAllByRole("button").map((button) => button.getAttribute("aria-label") ?? button.textContent ?? "");
        expect(names).toContain(i18n.t("composer.attachments.attachFiles", { ns: "app" }));
        expect(names.join(" ")).not.toMatch(/Thinking|Plan|Model|思考|计划|模型/);
        expect(screen.queryByText(/Extra High|Default/)).toBeNull();
    });
});
