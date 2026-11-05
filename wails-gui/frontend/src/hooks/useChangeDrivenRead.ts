import { useEffect, useRef } from 'react';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { appActive, onAppShown } from '@/lib/app-activity';

/** Change events that arrive within this window share one read. */
export const CHANGE_READ_COALESCE_MS = 250;

/**
 * The Today fetch rule for one binding (Bo, 2026-11-04):
 * - one read on mount, and none while the window is hidden or the Mac locked;
 * - one read per real change event, coalesced: a burst of events in
 *   CHANGE_READ_COALESCE_MS is one read, and an event that lands while a read
 *   is out schedules exactly one follow-up read when it settles;
 * - once each time the window is shown again (also a mount while hidden), so a
 *   shown card is never older than its last change.
 * No interval polling. Effects depend on the event names and `enabled` only.
 */
export function useChangeDrivenRead(
    enabled: boolean,
    events: readonly string[],
    read: () => unknown,
    accepts?: (data: unknown) => boolean,
): void {
    const latest = useRef(read);
    latest.current = read;
    const filter = useRef(accepts);
    filter.current = accepts;
    const key = events.join('|');
    useEffect(() => {
        if (!enabled) return;
        let timer: ReturnType<typeof setTimeout> | undefined;
        let inFlight = false;
        let dirty = false;
        let disposed = false;
        const run = () => {
            timer = undefined;
            if (disposed) return;
            // Hidden or locked: nothing runs; the next show reads once.
            if (!appActive()) return;
            if (inFlight) { dirty = true; return; }
            inFlight = true;
            Promise.resolve()
                .then(() => latest.current())
                .catch(() => undefined)
                .finally(() => {
                    inFlight = false;
                    if (dirty && !disposed) { dirty = false; schedule(); }
                });
        };
        const schedule = () => {
            if (disposed || timer !== undefined) return;
            timer = setTimeout(run, CHANGE_READ_COALESCE_MS);
        };
        schedule();
        const offs = events.map((event) => EventsOn(event, (data?: unknown) => {
            if (filter.current && !filter.current(data)) return;
            schedule();
        }));
        const offShown = onAppShown(() => schedule());
        return () => {
            disposed = true;
            if (timer !== undefined) clearTimeout(timer);
            offs.forEach((off) => off());
            offShown();
        };
    }, [key, enabled]);
}
