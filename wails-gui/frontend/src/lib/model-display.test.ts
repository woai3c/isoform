import { afterEach, describe, expect, it } from "vitest";
import i18n from "@/i18n";
import { displayModelCopy, displayModelText } from "./model-display";

afterEach(async () => { await i18n.changeLanguage("en"); });
describe("model presentation boundary", () => {
    it.each(["en", "zh-CN"])("renders masks and broken words in %s", async (language) => {
        await i18n.changeLanguage(language);
        for (const raw of [
            "the contract [REDACTED:account_number]sed out",
            "lap[REDACTED:account_number]sed out",
            "[REDACTED:private_email] and [REDACTED:future_kind]",
            "[redacted:account_number]", "[REDACTED:account_number", "[REDACTED",
        ]) {
            const text = displayModelText(raw);
            expect(text).not.toMatch(/\[redacted|number\]sed|numbersed|账号sed/i);
            expect(displayModelText(text)).toBe(text);
            expect(text).toContain(language === "en" ? "a" : "一");
        }
    });
    it("separates adjacent masks and punctuation", async () => {
        await i18n.changeLanguage("en");
        expect(displayModelText("Check:[REDACTED:account_number]")).toBe("Check: an account number");
        expect(displayModelText("[REDACTED:account_number]sed[REDACTED:api_key]")).toBe("an account number an API key");
    });
    it("preserves CJK neighbors, ordinary prose, IDs and cached source bytes", async () => {
        await i18n.changeLanguage("zh-CN");
        expect(displayModelText("请核对[REDACTED:account_number]后再继续")).toBe("请核对一个账号后再继续");
        const source = { id: "[REDACTED:account_number]", text: "Ask about [REDACTED:account_number]", nested: { label: "[REDACTED:api_key]" } };
        const shown = displayModelCopy(source);
        expect(shown.id).toBe(source.id);
        expect(shown.text).not.toContain("[REDACTED");
        expect(shown.nested.label).toBe("一个 API 密钥");
        expect(source.nested.label).toBe("[REDACTED:api_key]");
        expect(displayModelText("the contract lapsed out")).toBe("the contract lapsed out");
    });
});
