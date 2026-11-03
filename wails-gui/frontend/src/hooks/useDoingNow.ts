import { useEffect, useState } from "react";
import { GetDoingNow, type DoingNow } from "@/lib/wails-shim-app";
import { EventsOn } from "../../wailsjs/runtime/runtime";

/** Go emits the new DoingNow on this event whenever the line changes. */
export const DOING_NOW_CHANGED_EVENT = "doing-now:changed";

/**
 * The observingStatus.doingNow key for a line: the kind, or its "...Unnamed"
 * wording when the meeting app or task title it names is empty. Mirrors
 * doingNowCopyKey in Go, which words the same line in the menu bar.
 */
export function doingNowKey(view: DoingNow): string {
    if (view.kind === "meeting" && !view.app) return "meetingUnnamed";
    if (view.kind === "working" && !view.title) return "workingUnnamed";
    return view.kind;
}

/**
 * What Isoform is doing right now, the same line the menu bar shows. null
 * until read, and when it could not be read.
 */
export function useDoingNow(): DoingNow | null {
    const [view, setView] = useState<DoingNow | null>(null);

    useEffect(() => {
        let live = true;
        // A change event is newer than the first read, so once one arrives
        // a late answer to that read is dropped.
        let changed = false;
        GetDoingNow()
            .then((next) => {
                if (live && !changed) setView(next);
            })
            .catch(() => {});
        const off = EventsOn(DOING_NOW_CHANGED_EVENT, (next: DoingNow) => {
            if (!next?.kind) return;
            changed = true;
            setView(next);
        });
        return () => {
            live = false;
            off();
        };
    }, []);

    return view;
}
