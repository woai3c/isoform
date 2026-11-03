import { describe, expect, it } from "vitest";
import i18n from "@/i18n";
import type { MobileBriefDocument } from "@/lib/wails-shim-app";
import { briefHasWords, briefKind, briefLateLine, briefTitle } from "./brief-title";

const brief = (over: Partial<MobileBriefDocument>) => ({
    briefId: "2026-10-28-morning", slot: "morning", asOf: "2026-10-28T13:50:00Z", timezone: "UTC",
    narrativeStatus: "ok", narrative: [], ...over,
}) as unknown as MobileBriefDocument;

describe.each(["en", "zh-CN"])("brief titles in %s", (language) => {
    const tr = (key: string, options?: Record<string, unknown>) => String(i18n.t(key as never, { ...options, ns: "today", lng: language }));
    const en = language === "en";

    it("names the kind and the time the brief was made, as design section 1 does", () => {
        expect(briefTitle(brief({ slot: "morning", asOf: "2026-10-28T13:50:00Z" }), language, tr))
            .toBe(en ? "Morning brief · 1:50 PM" : "早间简报 · 下午1:50");
        expect(briefTitle(brief({ slot: "evening", asOf: "2026-10-28T18:00:00Z" }), language, tr))
            .toBe(en ? "Evening brief · 6:00 PM" : "晚间简报 · 下午6:00");
        expect(briefTitle(brief({ slot: "on_request", asOf: "2026-10-28T15:41:00Z" }), language, tr))
            .toBe(en ? "Brief · 3:41 PM" : "简报 · 下午3:41");
        expect(briefTitle(brief({ slot: "evening", asOf: "2026-10-28T18:00:00Z", yourWeek: {} as never }), language, tr))
            .toBe(en ? "Your week · 6:00 PM" : "你的一周 · 下午6:00");
    });

    it("names a brief asked for by the slot it stands in for, so the player names it (today-review v1.1.4 #7)", () => {
        const en = language === "en";
        expect(briefTitle(brief({ slot: "on_request", displaySlot: "morning", asOf: "2026-10-28T16:16:00Z" } as never), language, tr))
            .toBe(en ? "Morning brief · 4:16 PM" : "早间简报 · 下午4:16");
        expect(briefKind({ slot: "on_request", displaySlot: "evening" } as never)).toBe("evening");
        expect(briefKind({ slot: "on_request" } as never)).toBe("on_request");
    });

    it("titles a brief made late with its own made time, and adds the Late line", () => {
        const late = brief({
            slot: "morning", asOf: "2026-10-28T16:12:00Z",
            generation: { reason: "asleep", scheduledAt: "2026-10-28T13:50:00Z", generatedAt: "2026-10-28T16:12:00Z", message: "" },
        });
        expect(briefTitle(late, language, tr)).toBe(en ? "Morning brief · 4:12 PM" : "早间简报 · 下午4:12");
        const run = { briefId: late.briefId, status: "ok" as const, scheduledAt: "2026-10-28T13:50:00Z", at: late.asOf, late: true, reason: "asleep" as const };
        expect(briefLateLine(late, language, tr, run))
            .toBe(en ? "Came at 4:12 PM, not 1:50 PM: your computer was asleep." : "下午4:12才生成，不是1:50：电脑在睡眠。");
        expect(briefLateLine(late, language, tr)).toBeNull();
        expect(briefLateLine(late, language, tr, { ...run, briefId: "other" })).toBeNull();
        expect(briefLateLine(late, language, tr, { ...run, late: false })).toBeNull();
        expect(briefLateLine(late, language, tr, { ...run, reason: "closed" }))
            .toBe(en ? "Came at 4:12 PM, not 1:50 PM: Isoform wasn't open." : "下午4:12才生成，不是1:50：Isoform 没有打开。");
        expect(briefLateLine(late, language, tr, { ...run, reason: "" }))
            .toBe(en ? "Came at 4:12 PM, not 1:50 PM." : "下午4:12才生成，不是1:50。");

    });

    it("titles the brief with its matching completed run's ready time", () => {
        const doc = brief({ asOf: "2026-10-28T13:00:00Z" });
        const run = { briefId: doc.briefId, status: "ok" as const, scheduledAt: doc.asOf, at: "2026-10-28T16:23:00Z" };
        expect(briefTitle(doc, language, tr, run)).toBe(en ? "Morning brief · 4:23 PM" : "早间简报 · 下午4:23");
        expect(briefTitle(doc, language, tr, { ...run, briefId: "other" })).toBe(en ? "Morning brief · 1:00 PM" : "早间简报 · 下午1:00");
    });

    it("has no Late line for a brief made on time", () => {
        expect(briefLateLine(brief({}), language, tr)).toBeNull();
    });
});

describe("brief kind and words", () => {
    it("treats only morning and evening as scheduled kinds", () => {
        expect(briefKind(brief({ slot: "morning" }))).toBe("morning");
        expect(briefKind(brief({ slot: "evening" }))).toBe("evening");
        expect(briefKind(brief({ slot: "on_request" }))).toBe("on_request");
        expect(briefKind(brief({ slot: "morning", yourWeek: {} as never }))).toBe("week");
    });

    it("counts a brief as having words only with a paragraph that says something", () => {
        expect(briefHasWords(brief({ narrative: [] }))).toBe(false);
        expect(briefHasWords(brief({ narrative: [{ text: "   " } as never] }))).toBe(false);
        expect(briefHasWords(brief({ narrative: [{ text: "Three meetings today." } as never] }))).toBe(true);
        expect(briefHasWords(null)).toBe(false);
    });
});
