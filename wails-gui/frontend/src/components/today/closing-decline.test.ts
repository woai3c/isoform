import { describe, expect, it } from "vitest";
import { CLOSING_DECLINE } from "./TodayBriefSection";

// today-review v1.1.4 #6: every decline answer reads "Not now" / "暂不".
describe("closing decline labels", () => {
    it.each(["Not now", "Not today", "No, leave it.", "Leave it".replace("Leave it", "No"), "暂不", "先不", "不用，先放着", "不了"])("reads %s as a decline", (label) => {
        expect(CLOSING_DECLINE.test(label)).toBe(true);
    });
    it.each(["Yes, draft my reply", "Notes for Dana", "Nothing else", "好的，起草回复"])("keeps %s as written", (label) => {
        expect(CLOSING_DECLINE.test(label)).toBe(false);
    });
});
