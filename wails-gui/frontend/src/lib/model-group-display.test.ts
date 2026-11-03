import { describe, expect, it } from "vitest";
import i18n from "@/i18n";
import { modelGroupDisplayName } from "./model-group-display";

describe("modelGroupDisplayName", () => {
    it.each([
        ["yansu-chat", "yansu chat", "Isoform chat", "Isoform 对话"],
        ["yansu-background", "yansu background", "Isoform background", "Isoform 后台"],
        ["yansu-light", "yansu light", "Isoform light", "Isoform 轻量任务"],
    ])("localizes the original %s label without changing its ID", (id, name, english, chinese) => {
        const group = { id, name };
        expect(modelGroupDisplayName(group, i18n.getFixedT("en", "onboarding"))).toBe(english);
        expect(modelGroupDisplayName(group, i18n.getFixedT("zh-CN", "onboarding"))).toBe(chinese);
        expect(group).toEqual({ id, name });
    });

    it.each([
        [{ id: "my-chat", name: "yansu chat" }, "yansu chat"],
        [{ id: "yansu-chat", name: "My daily work" }, "My daily work"],
        [{ id: "yansu-chat-copy", name: "yansu chat" }, "yansu chat"],
        [{ id: "yansu-chat", name: "" }, "yansu-chat"],
    ])("preserves custom names and unknown IDs: %j", (group, expected) => {
        expect(modelGroupDisplayName(group, i18n.getFixedT("en", "onboarding"))).toBe(expected);
    });
});
