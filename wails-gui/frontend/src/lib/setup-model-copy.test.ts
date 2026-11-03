import { describe, expect, it } from "vitest";
import i18n from "@/i18n";
import source from "../../../../asr-eval/RESULTS.md?raw";
import { modelEvaluationTable } from "./setup-model-copy";

describe("localized model evaluation", () => {
    it.each(["en", "zh-CN"])(
        "keeps source table measurements in %s",
        async (language) => {
            await i18n.changeLanguage(language);
            const result = modelEvaluationTable(
                i18n.getFixedT(language, "onboarding"),
            );
            for (const heading of [
                "Word Error Rate (lower = better)",
                "Character Error Rate (lower = better)",
                "Real-Time Factor (lower = faster)",
                "Latency and Resources",
            ]) {
                const section = source
                    .split(`## ${heading}\n`)[1]
                    .split("\n## ")[0];
                const rows = section
                    .split("\n")
                    .filter((line) => line.startsWith("| "))
                    .slice(1);
                for (const row of rows) {
                    for (const value of row.split("|").slice(2, -1)) {
                        const measurement = value.trim().replaceAll("*", "");
                        if (measurement !== "N/A")
                            expect(result).toContain(measurement);
                    }
                }
            }
            expect(result).not.toContain("models.evaluationCopy.");
            expect(result.match(/^\| ---/gm)).toHaveLength(7);
        },
    );
});
