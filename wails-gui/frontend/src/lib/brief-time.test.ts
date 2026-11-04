import { describe, expect, it } from "vitest";
import i18n from "@/i18n";
import { briefClockTime, briefDateTime, briefSlotKey, clockTime, shortBriefClockLabel, shortBriefTimes } from "./brief-time";

describe.each(["en", "zh-CN"])("brief clocks in %s", (language) => {
    const tr = (key: string, options?: Record<string, unknown>) => String(i18n.t(key as never, { ...options, ns: "today", lng: language }));

    it.each([
        ["6:00 PM", "6:00 PM", "18:00"],
        ["1:30 PM", "1:30 PM", "13:30"],
        ["6pm", "6:00 PM", "18:00"],
        ["1:30pm", "1:30 PM", "13:30"],
        ["下午1:30", "1:30 PM", "13:30"],
        ["18:00:42", "18:00:42", "18:00:42"],
        ["01:04", "01:04", "01:04"],
        ["Use 16:10 displays", "Use 16:10 displays", "Use 16:10 displays"],
        ["aspect ratio 16:09", "aspect ratio 16:09", "aspect ratio 16:09"],
        ["Recording 1:05 to 1:10", "Recording 1:05 to 1:10", "Recording 1:05 to 1:10"],
        ["使用16:10显示器，录音1:05到1:10", "使用16:10显示器，录音1:05到1:10", "使用16:10显示器，录音1:05到1:10"],
        ["12:00 AM", "12:00 AM", "0:00"],
        ["12:00 PM", "12:00 PM", "12:00"],
        ["No clock in this past item", "No clock in this past item", "No clock in this past item"],
        ["2026-10-16T18:00:00Z", "2026-10-16T18:00:00Z", "2026-10-16T18:00:00Z"],
        ["2026-10-16T18:00:00-07:00", "2026-10-16T18:00:00-07:00", "2026-10-16T18:00:00-07:00"],
    ])("compacts %s without adding a clock", (input, en, zh) => {
        expect(shortBriefTimes(input, language, tr)).toBe(language === "en" ? en : zh);
    });

    it("uses the brief's timezone, with no seconds or zero-padded hour", () => {
        expect(briefClockTime("2026-10-17T01:00:42Z", language, tr, "America/Los_Angeles"))
            .toBe(language === "en" ? "6:00 PM" : "18:00");
        expect(briefClockTime("invalid", language, tr, "UTC")).toBeNull();
        expect(briefClockTime(undefined, language, tr)).toBeNull();
        expect(briefClockTime("2026-10-17T01:30:00Z", language, tr, "invalid"))
            .not.toMatch(/01:30|:00/);
        expect(briefDateTime("2026-10-17T01:30:00Z", language, tr, "Local"))
            .not.toMatch(/01:30|:00/);
        expect(briefDateTime(1e99, language, tr, "UTC")).toBeNull();
        expect(briefDateTime("2026-10-17T13:30:42Z", language, tr, "UTC"))
            .toBe(language === "en" ? "Oct 17, 1:30 PM" : "10月17日 13:30");
        expect(shortBriefClockLabel("Daily 09:00", language, tr))
            .toBe(language === "en" ? "Daily 9:00 AM" : "Daily 9:00");
        // A label already in the one clock is not rewritten twice.
        expect(shortBriefClockLabel("Once, Oct 15, 4:31 PM (UTC)", language, tr))
            .toBe(language === "en" ? "Once, Oct 15, 4:31 PM (UTC)" : "Once, Oct 15, 16:31 (UTC)");
        expect(shortBriefClockLabel("一次，10月15日 下午4:31 (UTC)", language, tr))
            .toBe(language === "en" ? "一次，10月15日 4:31 PM (UTC)" : "一次，10月15日 16:31 (UTC)");
    });

    it("formats a local time of day in the one clock", () => {
        expect(clockTime(new Date(2026, 9, 22, 7, 0), language, tr)).toBe(language === "en" ? "7:00 AM" : "7:00");
        expect(clockTime(new Date(2026, 9, 22, 12, 30), language, tr)).toBe(language === "en" ? "12:30 PM" : "12:30");
        expect(clockTime(new Date(2026, 9, 22, 14, 47), language, tr)).toBe(language === "en" ? "2:47 PM" : "14:47");
        expect(clockTime(new Date(2026, 9, 22, 0, 5), language, tr)).toBe(language === "en" ? "12:05 AM" : "0:05");
    });
});

describe("briefSlotKey (today-review v1.1.16 D14)", () => {
    it("names a brief asked for by its time: before noon morning, from noon evening", () => {
        expect(briefSlotKey("on_request", "2026-11-03T11:59:00Z", "UTC")).toBe("morning");
        expect(briefSlotKey("on_request", "2026-11-03T12:00:00Z", "UTC")).toBe("evening");
        expect(briefSlotKey("on_request", "2026-11-04T01:24:00Z", "America/Los_Angeles")).toBe("evening");
        expect(briefSlotKey("on_request", "2026-11-03T05:00:00Z", "UTC")).toBe("morning");
        expect(briefSlotKey("morning", "2026-11-04T01:24:00Z", "America/Los_Angeles")).toBe("morning");
    });
});
