import { describe, expect, it } from "vitest";
import { TODAY_PAGE_MIN_WIDTH, TOPIC_CARDS_MAX, TOPIC_PANEL_MIN_WIDTH, TOPIC_PANEL_PINNED_MIN_WIDTH, TOPIC_PANEL_WIDTH, isTopicCardName, topicActiveDays, topicCards, topicPanelWidth } from "./topics";
import type { TodayTopic } from "./topics";

describe("the pinned panel's width (Claude Design, 2026-10-22)", () => {
    it("pins from 920 px of space next to the sidebar", () => {
        expect(TOPIC_PANEL_PINNED_MIN_WIDTH).toBe(920);
        expect(TOPIC_PANEL_MIN_WIDTH).toBe(360);
        expect(TOPIC_PANEL_WIDTH).toBe(420);
        expect(TODAY_PAGE_MIN_WIDTH).toBe(560);
    });

    it.each([
        [920, 360, 560],
        [1010, 379, 631],
        [1120, 420, 700],
        [1400, 420, 980],
    ])("at %i px the panel is %i px and the page %i px", (space, panel, page) => {
        expect(topicPanelWidth(space)).toBe(panel);
        expect(space - topicPanelWidth(space)).toBe(page);
    });

    it("never takes the page under 560 px nor the panel under 360 or over 420", () => {
        for (let space = 920; space <= 2000; space += 7) {
            const panel = topicPanelWidth(space);
            expect(panel).toBeGreaterThanOrEqual(360);
            expect(panel).toBeLessThanOrEqual(420);
            expect(space - panel).toBeGreaterThanOrEqual(560);
        }
    });
});

describe("topic cards (today-review v1.1.4 #8)", () => {
    const now = new Date(2026, 9, 23, 12);
    const day = (back: number) => {
        const d = new Date(now.getFullYear(), now.getMonth(), now.getDate() - back);
        return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
    };
    const topic = (over: Partial<TodayTopic>): TodayTopic => ({ id: over.name ?? "t", name: "Atlas", kind: "work", pinned: false, ...over });

    it.each(["Atlas", "Linen trousers", "Riffado", "季度规划"])("keeps the human name %s", (name) => {
        expect(isTopicCardName(name)).toBe(true);
    });
    it.each(["ftb.ca.gov", "https://example.com/pay", "Inbox - Gmail", "Slack | general", "msg", "line one\nline two", "a very long five word name"])("rejects %s as a topic name", (name) => {
        expect(isTopicCardName(name)).toBe(false);
    });
    it("shows pinned topics first, then work on two days in the last seven, at most six", () => {
        const topics = [
            topic({ name: "One day", activityDays: [day(0)] }),
            topic({ name: "Old work", activityDays: [day(8), day(9)] }),
            ...Array.from({ length: 7 }, (_, i) => topic({ name: `Topic ${i}`, activityDays: [day(0), day(3)] })),
            topic({ name: "Pinned plan", pinned: true, activityDays: [] }),
        ];
        const names = topicCards(topics, now).map((t) => t.name);
        expect(names).toEqual(["Pinned plan", "Topic 0", "Topic 1", "Topic 2", "Topic 3", "Topic 4"]);
        expect(TOPIC_CARDS_MAX).toBe(6);
    });
    it("keeps a followed topic with something new, and counts distinct days once", () => {
        expect(topicActiveDays({ activityDays: [day(0), day(0), day(1)] }, now)).toBe(2);
        expect(topicCards([topic({ name: "Ramen", kind: "followed", activityDays: [day(0)], latestFind: { text: "A new shop opened" } })], now)).toHaveLength(1);
    });
});
