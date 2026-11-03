import { describe, expect, it } from "vitest";

import enSettings from "@/i18n/locales/en/settings.json";
import zhSettings from "@/i18n/locales/zh-CN/settings.json";
import {
    ACTIVITY_LEARNING_FEATURE_KEYS,
    ACTIVITY_LEARNING_FEATURE_ROWS,
} from "@/components/activity-learning-settings";

describe("Activity learning settings", () => {
    it("keeps the real Shadow observer but removes legacy work origination", () => {
        const keys = ACTIVITY_LEARNING_FEATURE_ROWS.map((row) => row.key);

        expect(keys).toEqual([
            "events",
            "memory",
            "knowledge",
            "shadow",
            "sop",
        ]);
        expect(keys).not.toContain("automation");
        expect(keys).not.toContain("handoff");
        expect(ACTIVITY_LEARNING_FEATURE_KEYS.shadow).toBe(
            "triage_enable_shadow",
        );
    });

    it("explains that observing never starts work in both languages", () => {
        expect(enSettings.activityTriage).toMatchObject({
            shadowTitle: "Learn by comparison",
        });
        expect(enSettings.activityTriage.cardDescription).toContain(
            "never starts work on its own",
        );
        expect(enSettings.activityTriage.cardDescription).not.toContain(
            "Independent work",
        );
        expect(zhSettings.activityTriage).toMatchObject({
            shadowTitle: "通过对比学习",
        });
        expect(zhSettings.activityTriage.cardDescription).toContain(
            "观察本身不会启动任何工作",
        );
        expect(zhSettings.activityTriage.cardDescription).not.toContain(
            "独立工作",
        );
        expect(enSettings.activityTriage).not.toHaveProperty("automationTitle");
        expect(enSettings.activityTriage).not.toHaveProperty("handoffTitle");
        expect(zhSettings.activityTriage).not.toHaveProperty("automationTitle");
        expect(zhSettings.activityTriage).not.toHaveProperty("handoffTitle");
    });
});
