import { describe, expect, it } from "vitest";
import enApp from "../../i18n/locales/en/app.json";
import zhCNApp from "../../i18n/locales/zh-CN/app.json";
import {
    handoffCriteria,
    handoffStatusKey,
    handoffVerification,
    handoffVerificationLine,
    isRetiredAgencyHandoff,
} from "./handoffStatus";

describe("handoffStatusKey", () => {
    it.each([
        ["active", "Waiting to start"], ["in_progress", "Working"],
        ["needs_review", "Needs review"], ["blocked", "Blocked"],
        ["completed", "Done"], ["aborted", "Stopped"], ["stale", "Expired"],
    ] as const)("uses the product lifecycle for %s in both locales", (status, label) => {
        expect(handoffStatusKey({ status })).toBe(`handoffsPage.status.${status}`);
        expect(enApp.handoffsPage.status[status]).toBe(label);
        expect(zhCNApp.handoffsPage.status[status]).toBeTruthy();
        expect(zhCNApp.handoffsPage.status[status]).not.toBe(label);
    });

    it("maps needs_review to its own label key", () => {
        expect(handoffStatusKey({ status: "needs_review" })).toBe(
            "handoffsPage.status.needs_review",
        );
    });

    it("labels retired Isoform-originated work as no longer available", () => {
        expect(handoffStatusKey({ status: "stale", source: "agency" })).toBe(
            "handoffsPage.status.unavailable",
        );
        expect(handoffStatusKey({ status: "stale", source: "fallback" })).toBe(
            "handoffsPage.status.stale",
        );
        expect(handoffStatusKey({ status: "stale" })).toBe(
            "handoffsPage.status.stale",
        );
    });

    it("labels other Isoform-originated statuses like any other entry", () => {
        const agencyEntry = (status: string) => ({ status, source: "agency" });
        expect(handoffStatusKey(agencyEntry("active"))).toBe(
            "handoffsPage.status.active",
        );
        expect(handoffStatusKey(agencyEntry("blocked"))).toBe(
            "handoffsPage.status.blocked",
        );
    });
});

describe("isRetiredAgencyHandoff", () => {
    it("is true only for stale Isoform-originated work", () => {
        expect(isRetiredAgencyHandoff({ status: "stale", source: "agency" })).toBe(
            true,
        );
        expect(
            isRetiredAgencyHandoff({ status: "active", source: "agency" }),
        ).toBe(false);
        expect(
            isRetiredAgencyHandoff({ status: "stale", source: "fallback" }),
        ).toBe(false);
    });
});

describe("handoffVerification", () => {
    it("reads a complete verification result", () => {
        expect(
            handoffVerification({
                verification: { met: 2, total: 3, note: "" },
            }),
        ).toEqual({ met: 2, total: 3, note: "" });
    });

    it("returns null when the backend has not verified the run", () => {
        expect(handoffVerification({})).toBeNull();
        expect(handoffVerification({ verification: null })).toBeNull();
    });

    it("rejects a result whose counts are not numbers", () => {
        expect(
            handoffVerification({ verification: { met: "2", total: 3 } }),
        ).toBeNull();
    });
});

describe("handoffVerificationLine", () => {
    it("summarises the checks when there is a total", () => {
        expect(handoffVerificationLine({ met: 2, total: 3 })).toEqual({
            kind: "checks",
            key: "handoffsPage.verification.summary",
            met: 2,
            total: 3,
        });
    });

    it("never displays legacy backend notes as untranslated text", () => {
        expect(
            handoffVerificationLine({
                met: 0,
                total: 0,
                note: "nothing was verifiable",
            }),
        ).toEqual({ kind: "none", key: "handoffsPage.verification.none" });
    });

    it.each([undefined, "", "   "])(
        "falls back to a fixed line when the note is %j",
        (note) => {
            expect(handoffVerificationLine({ met: 0, total: 0, note })).toEqual(
                {
                    kind: "none",
                    key: "handoffsPage.verification.none",
                },
            );
        },
    );

    it("returns null without a verification result", () => {
        expect(handoffVerificationLine(null)).toBeNull();
    });
});

describe("handoffCriteria", () => {
    it("keeps only non-empty strings", () => {
        expect(
            handoffCriteria({ criteria: ["Draft is saved", " ", 3, "Tests pass"] }),
        ).toEqual(["Draft is saved", "Tests pass"]);
    });

    it("returns an empty list for the backend's null slice or a missing field", () => {
        expect(handoffCriteria({ criteria: null })).toEqual([]);
        expect(handoffCriteria({})).toEqual([]);
    });
});

describe("needs_review locale parity", () => {
    it("has the new handoff keys in English and Simplified Chinese", () => {
        expect(enApp.handoffsPage.status.needs_review).toBe("Needs review");
        expect(zhCNApp.handoffsPage.status.needs_review).toBe("待审阅");
        expect(enApp.handoffsPage.verification.summary).toBe(
            "{{met}} of {{total}} checks met",
        );
        expect(zhCNApp.handoffsPage.verification.summary).toBe(
            "{{total}} 项检查中有 {{met}} 项通过",
        );
        expect(enApp.handoffsPage.verification.none).toBeTruthy();
        expect(zhCNApp.handoffsPage.verification.none).toBeTruthy();
        expect(enApp.handoffsPage.criteria).toBeTruthy();
        expect(zhCNApp.handoffsPage.criteria).toBeTruthy();
        expect(enApp.handoffsPage.needsReviewTitle).toBeTruthy();
        expect(zhCNApp.handoffsPage.needsReviewTitle).toBeTruthy();
        expect(enApp.handoffsPage.needsReviewBody).toBeTruthy();
        expect(zhCNApp.handoffsPage.needsReviewBody).toBeTruthy();
    });

    it("has the retired-work keys in both languages", () => {
        expect(enApp.handoffsPage.status.unavailable).toBe(
            "No longer available",
        );
        expect(zhCNApp.handoffsPage.status.unavailable).toBe("已不可用");
        expect(enApp.handoffsPage.agencyUnavailableBody).toBe(
            "The work this item was based on is no longer current. Isoform kept the plan here for reference.",
        );
        expect(zhCNApp.handoffsPage.agencyUnavailableBody).toBe(
            "此项所依据的工作已不再有效。Isoform 将计划保留在这里供参考。",
        );
        expect(enApp.handoffsPage.agencyUnavailableTitle).toBeTruthy();
        expect(zhCNApp.handoffsPage.agencyUnavailableTitle).toBeTruthy();
    });

});

describe("verification reason codes", () => {
    it.each(["private_review", "no_criteria", "invalid_result", "incomplete_result", "invalid_evidence", "check_unavailable", "invalid_check_reply", "criteria_unconfirmed"])("localizes %s without backend text", (reason) => {
        expect(handoffVerificationLine({ met: 0, total: 0, reason, note: "untrusted backend text" } )).toEqual({ kind: "reason", key: `handoffsPage.verification.reason.${reason}` });
        const key = reason as keyof typeof enApp.handoffsPage.verification.reason;
        expect(enApp.handoffsPage.verification.reason[key]).toBeTruthy();
        expect(zhCNApp.handoffsPage.verification.reason[key]).toBeTruthy();
        expect(enApp.handoffsPage.verification.reason[key]).not.toBe(zhCNApp.handoffsPage.verification.reason[key]);
    });
});
