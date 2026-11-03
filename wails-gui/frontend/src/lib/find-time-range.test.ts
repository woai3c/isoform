import { describe, expect, it } from "vitest";
import { findTimeRangeFor } from "./find-time-range";

// Thursday, 29 October 2026, local time.
const NOW = new Date(2026, 9, 29, 10, 0);

describe("findTimeRangeFor", () => {
    it.each([
        ["Find time for the memo on Friday", { first: "2026-10-30", last: "2026-10-30", minutes: 60 }],
        ["Find 2 hours for the memo tomorrow", { first: "2026-10-30", last: "2026-10-30", minutes: 120 }],
        ["Find time for the memo next Tuesday", { first: "2026-11-03", last: "2026-11-03", minutes: 60 }],
        ["Find time next week", { first: "2026-11-02", last: "2026-11-06", minutes: 60, nextWeek: true }],
        ["Find time this week", { first: "2026-10-29", last: "2026-10-30", minutes: 60 }],
        ["Find 30 minutes today", { first: "2026-10-29", last: "2026-10-29", minutes: 30 }],
        ["为备忘录在周五留出时间", { first: "2026-10-30", last: "2026-10-30", minutes: 60 }],
        ["下周为备忘录留出时间", { first: "2026-11-02", last: "2026-11-06", minutes: 60, nextWeek: true }],
        ["下周二留出 1 小时", { first: "2026-11-03", last: "2026-11-03", minutes: 60 }],
        ["本周留出半小时", { first: "2026-10-29", last: "2026-10-30", minutes: 30 }],
    ])("reads %s as a date range", (text, range) => {
        expect(findTimeRangeFor(text, NOW)).toEqual(range);
    });

    it.each(["Find time for the memo", "为备忘录留出时间"])("does not guess a range for %s", (text) => {
        expect(findTimeRangeFor(text, NOW)).toBeNull();
    });
});
