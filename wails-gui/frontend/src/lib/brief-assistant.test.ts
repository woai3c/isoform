import { describe, expect, it } from "vitest";
import i18n from "@/i18n";
import { briefAssistantName } from "./brief-assistant";

describe("briefAssistantName (R75)", () => {
    it.each([
        ["en", "claude-code", "Claude Code"],
        ["en", "codex", "Codex"],
        ["en", undefined, "Your AI"],
        ["zh-CN", "claude-code", "Claude Code"],
        ["zh-CN", "", "你的 AI"],
    ])("names %s assistant %s as %s", async (language, assistant, expected) => {
        await i18n.changeLanguage(language);
        expect(briefAssistantName(assistant, i18n.getFixedT(language, "app"))).toBe(expected);
        await i18n.changeLanguage("en");
    });
});
