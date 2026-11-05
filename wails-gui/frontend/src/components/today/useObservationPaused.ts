import { useEffect, useState } from "react";
import { IsClosedEyesModeEnabled } from "@/lib/page-queries";
import { pageQueryCache } from "@/lib/query-cache";
import { EventsOn } from "../../../wailsjs/runtime/runtime";

/** Go emits this from every surface that pauses or resumes observation (the
 *  menu bar, the tray, the hotkey, the overlay and Settings). */
const CLOSED_EYES_CHANGED_EVENT = "closed-eyes:changed";

/** Whether observation is paused (Closed Eyes): null until it is known. Today
 *  reads no sources for chat while it is paused or unknown. */
export function useObservationPaused(): boolean | null {
    // A warm cache (Today read it at startup) answers synchronously.
    const [paused, setPaused] = useState<boolean | null>(() => {
        const cached = pageQueryCache.peek<boolean>('IsClosedEyesModeEnabled');
        return cached === undefined ? null : Boolean(cached);
    });
    useEffect(() => {
        let alive = true;
        const reload = () => {
            IsClosedEyesModeEnabled()
                .then((value) => { if (alive) setPaused(Boolean(value)); })
                .catch(() => {});
        };
        reload();
        // Showing the window invalidates Today's reads (page-queries), which covers
        // this one; focus alone does not re-read it.
        const off = EventsOn(CLOSED_EYES_CHANGED_EVENT, (next: unknown) => setPaused(Boolean(next)));
        return () => {
            alive = false;
            off();
        };
    }, []);
    return paused;
}
