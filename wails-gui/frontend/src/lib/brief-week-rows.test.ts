import { describe, expect, it } from "vitest";
import { weekRowsShown } from "./brief-week-rows";

// The rows the Your week view draws (design PR #664): what a row would render, not what the data holds.
describe("weekRowsShown", () => {
    it("drops time bars when they total no hours, and done rows the view cannot open", () => {
        const shown = weekRowsShown({
            start: "2026-10-26", end: "2026-11-01", lead: "Your week",
            time: [{ name: "Brief work", hours: 0 }],
            done: [
                { id: "a", kind: "deliverable", title: "Doc" },
                { id: "b", kind: "deliverable", title: "Doc", source: "s", sourceId: "1", path: "/x" },
                { id: "c", kind: "outcome", title: "Task", taskId: "t1" },
            ],
        } as never, false);
        expect(shown.time).toEqual([]);
        expect(shown.done.map((row) => row.id)).toEqual(["b"]);
    });

    it("keeps a task row only where the view can open tasks", () => {
        const week = { start: "", end: "", lead: "", done: [{ id: "c", kind: "outcome", taskId: "t1" }] } as never;
        expect(weekRowsShown(week, true).done).toHaveLength(1);
        expect(weekRowsShown(week, false).done).toHaveLength(0);
    });
});
