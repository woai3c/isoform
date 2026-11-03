import { describe, expect, it } from "vitest";
import { parseOpenAnswer } from "./open-answers";

describe("open answer boundary", () => {
    const record = {
        id: "answer", sessionId: "chat", messageId: "message", question: "Did Priya reply?",
        kind: "replies", settles: "reply", source: "Mail", watchedEvent: { who: "Priya", label: "reply" },
        delivery: "around", expiresAt: "2026-11-02T00:00:00Z", posts: 0,
        state: "open", createdAt: "2026-10-26T00:00:00Z",
    };
    it("accepts the backend shape and rejects malformed dates and states", () => {
        expect(parseOpenAnswer(record)).toEqual(record);
        expect(parseOpenAnswer({ ...record, createdAt: 1 })).toBeUndefined();
        expect(parseOpenAnswer({ ...record, state: "unknown" })).toBeUndefined();
    });

    it("accepts and preserves the optional learned flag", () => {
        expect(parseOpenAnswer({ ...record, learned: true })?.learned).toBe(true);
        expect(parseOpenAnswer({ ...record, learned: false })?.learned).toBe(false);
        expect(parseOpenAnswer({ ...record, learned: "yes" })).toBeUndefined();
    });
});
