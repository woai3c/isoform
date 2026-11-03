import { useCallback, useEffect, useRef, useState } from "react";
import { GetUpcoming, type UpcomingItem } from "@/lib/wails-shim-app";
import { EventsOn } from "../../wailsjs/runtime/runtime";

/** Coalesces bursts of schedule events (a run start, its state, its end)
 *  into one reload. */
const RELOAD_DEBOUNCE_MS = 400;

export interface UpcomingData {
    items: UpcomingItem[];
    loading: boolean;
    /** The last read failed; the list shown is the one before it. */
    failed: boolean;
    refresh: () => void;
}

/**
 * The Upcoming list (GetUpcoming): everything Isoform will do on its own,
 * ordered by next run. Reloads when a schedule changes or runs, when the
 * brief schedule or a brief changes, and on focus.
 */
export function useUpcoming(enabled: boolean): UpcomingData {
    const [items, setItems] = useState<UpcomingItem[]>([]);
    const [loading, setLoading] = useState(true);
    const [failed, setFailed] = useState(false);
    // Bumped by every reload and on unmount; an answer whose version is
    // stale when it resolves is dropped, so overlapping reloads cannot leave
    // the older list on screen.
    const version = useRef(0);

    const refresh = useCallback(() => {
        const current = ++version.current;
        GetUpcoming()
            .then((next) => {
                if (current !== version.current) return;
                setItems(next ?? []);
                setFailed(false);
                setLoading(false);
            })
            .catch(() => {
                if (current !== version.current) return;
                setFailed(true);
                setLoading(false);
            });
    }, []);

    useEffect(() => {
        if (!enabled) return;
        refresh();
        let timer: ReturnType<typeof setTimeout> | null = null;
        const scheduleReload = () => {
            if (timer) clearTimeout(timer);
            timer = setTimeout(() => {
                timer = null;
                refresh();
            }, RELOAD_DEBOUNCE_MS);
        };
        const unsubscribers = ["cron:state", "cron:run", "mobile:updated"].map((event) =>
            EventsOn(event, scheduleReload),
        );
        window.addEventListener("focus", scheduleReload);
        return () => {
            version.current += 1;
            if (timer) clearTimeout(timer);
            unsubscribers.forEach((unsubscribe) => unsubscribe());
            window.removeEventListener("focus", scheduleReload);
        };
    }, [enabled, refresh]);

    return { items, loading, failed, refresh };
}
