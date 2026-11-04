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
            expect(text).toContain(language === "en" || /[a-z]{2}/i.test(raw.replace(/\[redacted[^\]]*\]?/gi, "")) ? "a" : "一");
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
    it("rewrites the sentence around a private item (today-review v1.1.16 D15)", async () => {
        await i18n.changeLanguage("en");
        expect(displayModelText("The [REDACTED:code] release is stalled.")).toBe("A private release is stalled.");
        expect(displayModelText("Ask about the [REDACTED:code] release today.")).toBe("Ask about a private release today.");
        expect(displayModelText("Your verification code is [REDACTED:code].")).toBe("Your verification code is a private code.");
        expect(displayModelText("Check the [REDACTED:account_number].")).toBe("Check an account number.");
        expect(displayModelText(displayModelText("The [REDACTED:code] release is stalled."))).toBe("A private release is stalled.");
        await i18n.changeLanguage("zh-CN");
        expect(displayModelText("这个[REDACTED:code]发布卡住了。")).toBe("一个私密代码发布卡住了。");
    });
    it("rewrites around a private item in the brief's own language (today-review v1.1.20 R46)", async () => {
        await i18n.changeLanguage("zh-CN");
        expect(displayModelText("The [REDACTED:code] release is stalled, and Maya is waiting on it before Thursday.")).toBe("A private release is stalled, and Maya is waiting on it before Thursday.");
        expect(displayModelText("[REDACTED:api_key]")).toBe("一个 API 密钥");
        await i18n.changeLanguage("en");
        expect(displayModelText("这个[REDACTED:code]发布卡住了。")).toBe("一个私密代码发布卡住了。");
        expect(displayModelText("[REDACTED:api_key]")).toBe("an API key");
    });
});
