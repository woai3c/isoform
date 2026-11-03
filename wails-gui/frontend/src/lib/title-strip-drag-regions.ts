import { isWailsEnvironment } from "@/lib/browser-bridge";
import {
    SetTitleStripDragRegions,
    type TitleStripRect,
} from "@/lib/wails-shim-app";

// Tells the native side where the title strip drags the window, so the
// first click on an inactive window is passed to the page there and only
// there (webview_title_strip_darwin.go). The strip is sampled on a grid of
// small cells: a cell is draggable when the elements under all four of its
// corners compute --wails-draggable: drag, so a control, a dialog's overlay
// or the onboarding screen anywhere in a cell leaves it out. The regions
// matter only while the window is inactive, so the strip is sampled when
// the window loses focus and on changes while it stays out of focus.

/** The title strip's height (App.tsx h-[44px]). */
const STRIP_HEIGHT = 44;
const ROW_HEIGHT = 11;
const CELL_WIDTH = 8;
const PUBLISH_DELAY_MS = 300;

/** The strip's draggable areas, one rect per run of draggable cells in a row. */
export function sampleTitleStripDragRects(
    width: number,
    isDraggable: (x: number, y: number) => boolean,
): TitleStripRect[] {
    const rects: TitleStripRect[] = [];
    const cellDraggable = (left: number, top: number) => {
        const right = Math.min(left + CELL_WIDTH, width) - 1;
        const bottom = top + ROW_HEIGHT - 1;
        return (
            isDraggable(left, top) &&
            isDraggable(right, top) &&
            isDraggable(left, bottom) &&
            isDraggable(right, bottom)
        );
    };
    for (let top = 0; top < STRIP_HEIGHT; top += ROW_HEIGHT) {
        let start = -1;
        for (let x = 0; x < width; x += CELL_WIDTH) {
            const drag = cellDraggable(x, top);
            if (drag && start < 0) start = x;
            if (!drag && start >= 0) {
                rects.push({ x: start, y: top, width: x - start, height: ROW_HEIGHT });
                start = -1;
            }
        }
        if (start >= 0) rects.push({ x: start, y: top, width: width - start, height: ROW_HEIGHT });
    }
    return rects;
}

function draggableAt(cache: Map<Element, boolean>) {
    return (x: number, y: number) => {
        const el = document.elementFromPoint(x, y);
        if (!el) return false;
        let drag = cache.get(el);
        if (drag === undefined) {
            drag = getComputedStyle(el).getPropertyValue("--wails-draggable").trim() === "drag";
            cache.set(el, drag);
        }
        return drag;
    };
}

/** Keeps the native side's copy current while the window is out of focus; returns a stop function. */
export function startTitleStripDragRegions(): () => void {
    if (!isWailsEnvironment()) return () => {};
    let last = "";
    let timer: ReturnType<typeof setTimeout> | null = null;
    const publish = () => {
        if (timer !== null) clearTimeout(timer);
        timer = null;
        const rects = sampleTitleStripDragRects(window.innerWidth, draggableAt(new Map()));
        const key = JSON.stringify(rects);
        if (key === last) return;
        last = key;
        SetTitleStripDragRegions(rects).catch(() => {
            last = "";
        });
    };
    // Changes while the window has focus wait for the next blur.
    const changed = () => {
        if (document.hasFocus() || timer !== null) return;
        timer = setTimeout(publish, PUBLISH_DELAY_MS);
    };
    const observer = new MutationObserver(changed);
    observer.observe(document.body, {
        subtree: true,
        childList: true,
        characterData: true,
        attributes: true,
        attributeFilter: ["class", "style", "data-state"],
    });
    window.addEventListener("blur", publish);
    window.addEventListener("resize", changed);
    if (!document.hasFocus()) publish();
    return () => {
        observer.disconnect();
        window.removeEventListener("blur", publish);
        window.removeEventListener("resize", changed);
        if (timer !== null) clearTimeout(timer);
    };
}
