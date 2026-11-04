import { describe, expect, it } from "vitest";
import enOnboarding from "./locales/en/onboarding.json";
import zhOnboarding from "./locales/zh-CN/onboarding.json";
import enSettings from "./locales/en/settings.json";
import zhSettings from "./locales/zh-CN/settings.json";

describe("usage count disclosures", () => {
    it("names the random install ID without calling the linked counts anonymous", () => {
        for (const [onboarding, settings] of [[enOnboarding, enSettings], [zhOnboarding, zhSettings]] as const) {
            expect(onboarding.telemetry.description).toMatch(/random install ID|随机生成的安装 ID/);
            expect(settings.privacy.diagnostics.description).toMatch(/random install ID|随机生成的安装 ID/);
            expect(JSON.stringify(onboarding)).not.toMatch(/anonymous|匿名/i);
            expect(settings.privacy.diagnostics.description).not.toMatch(/anonymous|匿名/i);
        }
    });
});
