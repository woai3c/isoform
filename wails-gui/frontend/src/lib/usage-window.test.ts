import { describe, expect, it } from "vitest";
import i18n from "@/i18n";
import { usageWindowLimit } from "./usage-window";

// ST61 (settings-review v1.0.1): a usage window reads as people say it, never
// with the raw label or the model's name.
describe("usageWindowLimit", () => {
    it("names the window in English", async () => {
        await i18n.changeLanguage("en");
        const t = i18n.getFixedT("en", "settings");
        expect(usageWindowLimit("7 days · Fable", t)).toBe("weekly limit");
        expect(usageWindowLimit("7 days", t)).toBe("weekly limit");
        expect(usageWindowLimit("1 day", t)).toBe("daily limit");
        expect(usageWindowLimit("24 hours", t)).toBe("daily limit");
        expect(usageWindowLimit("5 hours", t)).toBe("5-hour limit");
        expect(usageWindowLimit("30 days", t)).toBe("30-day limit");
        expect(usageWindowLimit("Weekly · Fable", t)).toBe("weekly limit");
        expect(usageWindowLimit("Fable", t)).toBeNull();
        expect(usageWindowLimit("", t)).toBeNull();
    });

    it("names the window in Chinese", () => {
        const t = i18n.getFixedT("zh-CN", "settings");
        expect(usageWindowLimit("7 days · Fable", t)).toBe("每周额度");
        expect(usageWindowLimit("1 day", t)).toBe("每日额度");
        expect(usageWindowLimit("5 hours", t)).toBe("5 小时额度");
    });
});
