import type { MobileBriefWeek } from "@/lib/wails-shim-app";

/** The week rows the Your week view draws (design PR #664 review). A row that
 *  exists but would not draw does not count: a time bar needs hours, a done row
 *  needs a file it opens or a task the view can open. */
export function weekRowsShown(week: MobileBriefWeek | undefined, canOpenTask: boolean) {
    const time = week?.time ?? [];
    const totalHours = time.reduce((sum, row) => sum + row.hours, 0);
    return {
        time: totalHours > 0 ? time : [],
        done: (week?.done ?? []).filter((row) =>
            (row.kind === "deliverable" && !!row.source && !!row.sourceId && !!row.path)
            || (!!row.taskId && canOpenTask)),
        open: week?.open ?? [],
        learned: week?.learned ?? [],
    };
}
