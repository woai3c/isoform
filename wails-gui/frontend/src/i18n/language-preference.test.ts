import { afterEach, beforeEach, describe, expect, it } from "vitest";
import i18n, { getLanguagePreference, setLanguagePreference } from "@/i18n";

describe("language preference", () => {
    const originalLanguages = Object.getOwnPropertyDescriptor(window.navigator, "languages");

    beforeEach(() => {
        localStorage.clear();
        Object.defineProperty(window.navigator, "languages", { value: ["zh-CN", "en"], configurable: true });
    });

    afterEach(async () => {
        if (originalLanguages) Object.defineProperty(window.navigator, "languages", originalLanguages);
        localStorage.clear();
        await i18n.changeLanguage("en");
    });

    it("is System default until a language is picked", () => {
        expect(getLanguagePreference()).toBe("system");
    });

    it("remembers an explicit language", async () => {
        await setLanguagePreference("en");
        expect(localStorage.getItem("i18nextLng")).toBe("en");
        expect(getLanguagePreference()).toBe("en");
        expect(i18n.resolvedLanguage).toBe("en");
    });

    it("forgets the choice and follows the computer for System default", async () => {
        await setLanguagePreference("en");
        await setLanguagePreference("system");
        expect(localStorage.getItem("i18nextLng")).toBeNull();
        expect(getLanguagePreference()).toBe("system");
        expect(i18n.resolvedLanguage).toBe("zh-CN");
    });

    it("does not store a language when one is only switched in code", async () => {
        await i18n.changeLanguage("zh-CN");
        expect(localStorage.getItem("i18nextLng")).toBeNull();
    });
});
