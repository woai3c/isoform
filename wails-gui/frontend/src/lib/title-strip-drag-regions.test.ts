import { describe, expect, it } from "vitest";
import { sampleTitleStripDragRects } from "./title-strip-drag-regions";

describe("sampleTitleStripDragRects", () => {
    it("reports the draggable runs of each row and leaves controls out", () => {
        // A 100px wide strip: a button at x 40..56 in the middle rows.
        const button = (x: number, y: number) => x >= 40 && x < 56 && y >= 11 && y < 33;
        const rects = sampleTitleStripDragRects(100, (x, y) => !button(x, y));
        expect(rects).toEqual([
            { x: 0, y: 0, width: 100, height: 11 },
            { x: 0, y: 11, width: 40, height: 11 },
            { x: 56, y: 11, width: 44, height: 11 },
            { x: 0, y: 22, width: 40, height: 11 },
            { x: 56, y: 22, width: 44, height: 11 },
            { x: 0, y: 33, width: 100, height: 11 },
        ]);
    });

    it("leaves out every cell a control reaches into, not only the ones it covers the center of", () => {
        // A button at x 43..51 reaches into the cells at 40 and 48.
        const button = (x: number, y: number) => x >= 43 && x < 51 && y >= 11 && y < 22;
        const rects = sampleTitleStripDragRects(64, (x, y) => !button(x, y));
        expect(rects.filter((r) => r.y === 11)).toEqual([
            { x: 0, y: 11, width: 40, height: 11 },
            { x: 56, y: 11, width: 8, height: 11 },
        ]);
    });

    it("reports nothing when a dialog covers the strip", () => {
        expect(sampleTitleStripDragRects(1200, () => false)).toEqual([]);
    });
});
