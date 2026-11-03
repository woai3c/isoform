import { describe, expect, it } from "vitest";
import {
    backgroundLearningRunStatusLabel,
    backgroundLearningRunSummary,
    backgroundLearningRunTriggerText,
    backgroundLearningSourceDescription,
    backgroundLearningSourceLastResult,
    backgroundLearningStatusText,
    backgroundLearningUsageText,
} from "./background-learning-presentation";
import type { BackgroundUnderstandingSnapshot } from "@/lib/wails-shim-app";

describe("background learning product language", () => {
    it.each([
        ["unavailable", "Background learning is unavailable right now."],
        ["paused", "Background learning is paused."],
        ["waiting", "Isoform is waiting for enough new context to learn from."],
        [
            "waiting_for_source",
            "Isoform is waiting for an allowed source to become available.",
        ],
        ["ready", "Isoform is ready to learn from allowed sources."],
        ["working", "Isoform is learning from allowed sources now."],
        [
            "limit_reached",
            "Today's learning checks are complete. Isoform will continue after the daily limit resets.",
        ],
    ] satisfies Array<
        [BackgroundUnderstandingSnapshot["status"], string]
    >)("translates %s without scheduler language", (status, expected) => {
        expect(backgroundLearningStatusText(status)).toBe(expected);
        expect(backgroundLearningStatusText(status)).not.toMatch(
            /bounded|mission|scheduler|run/i,
        );
    });

    it("keeps singular and plural daily usage grammatical", () => {
        expect(
            backgroundLearningUsageText({ todayRunCount: 0, dailyRunLimit: 1 }),
        ).toBe("0 of 1 check today");
        expect(
            backgroundLearningUsageText({ todayRunCount: 1, dailyRunLimit: 4 }),
        ).toBe("1 of 4 checks today");
        expect(
            backgroundLearningUsageText({ todayRunCount: 4, dailyRunLimit: 4 }),
        ).toBe("4 of 4 checks today");
    });

    it("keeps source failures useful without exposing backend errors", () => {
        const gmail = {
            source: "gmail" as const,
            label: "Gmail",
            available: false,
            connected: false,
            allowed: true,
            scopeLabel: "Work and personal correspondence",
            lastRunState: "failed",
            unavailableReason: "raw_cua_transport_failure",
            lastRunSummary: "raw_bounded_gmail_error",
        };

        expect(backgroundLearningSourceDescription(gmail)).toBe(
            "Open Gmail in a signed-in browser window, then try again.",
        );
        expect(backgroundLearningSourceLastResult(gmail)).toBe(
            "Isoform could not read Gmail. Open it in a signed-in browser window, then try again.",
        );
    });

    it("translates immutable run state without exposing raw summaries or triggers", () => {
        const run = {
            id: "run-1",
            missionKind: "retained_activity" as const,
            source: "local_activity" as const,
            state: "failed" as const,
            triggerKind: "automatic" as const,
            triggerReason: "raw_bounded_scheduler_reason",
            startedAt: "2026-08-06T12:00:00Z",
            updatedAt: "2026-08-06T12:01:00Z",
            summary: "raw_failure_stack",
            sourceItemCount: 0,
            materializationCount: 0,
        };

        expect(backgroundLearningRunSummary(run)).toBe(
            "Isoform could not finish this learning check.",
        );
        expect(backgroundLearningRunStatusLabel(run.state)).toBe(
            "Could not finish",
        );
        expect(backgroundLearningRunTriggerText(run)).toBe(
            "Isoform found a quiet window for allowed background learning.",
        );
    });
});
