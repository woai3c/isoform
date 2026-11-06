import { useEffect, useMemo, useState } from 'react';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { IdeaMeaningGroups } from '@/lib/wails-shim-app';

/** The app's event for "meaning groups finished building" (see idea_meaning.go). */
const READY_EVENT = 'idea-meanings:ready';

/**
 * The meaning group of each item, from the app's embedder, aligned with items
 * (design review v1.0.8 W40, Y33; v1.0.9 R24). The page paints with its word
 * matcher at once. When the app answers pending, the hook re-reads once when the
 * app says the groups are ready. It never re-reads on a timer. Null when the app
 * has no embedder or the groups have not arrived; the page keeps the word matcher.
 */
export function useIdeaMeanings<T>(
    items: readonly T[],
    titleOf: (item: T) => string,
    savedAtOf: (item: T) => number,
    /** The cosine threshold: how alike two titles must be to share a group. */
    threshold: number,
    /** The text the embedder reads for an item (title, or title and takeaway). */
    textOf: (item: T) => string = titleOf,
): number[] | null {
    const order = useMemo(
        () => items.map((_, index) => index).sort((a, b) => savedAtOf(items[b]) - savedAtOf(items[a]) || a - b),
        [items, savedAtOf],
    );
    const titles = useMemo(() => order.map((index) => textOf(items[index])), [items, order, textOf]);
    const key = useMemo(() => titles.join('\n'), [titles]);
    const [loaded, setLoaded] = useState<{ key: string; groups: number[] | null } | null>(null);
    // Bumped once per ready event; each bump re-reads the groups one time.
    const [ready, setReady] = useState(0);
    useEffect(() => {
        if (titles.length === 0) return;
        let cancelled = false;
        let unsubscribe: (() => void) | undefined;
        let waiting = false;
        const read = () => {
            let pending: Promise<{ groups: number[] | null; pending: boolean } | null>;
            try {
                pending = IdeaMeaningGroups(titles, threshold);
            } catch {
                // A missing binding (an older build, or a test double without it) falls back too.
                pending = Promise.resolve(null);
            }
            pending
                .then((answer) => {
                    if (cancelled) return;
                    if (answer?.groups) {
                        waiting = false;
                        setLoaded({ key, groups: answer.groups });
                    } else if (answer?.pending) {
                        // Still building: wait for the ready event, with no timer.
                        waiting = true;
                    } else {
                        waiting = false;
                        setLoaded({ key, groups: null });
                    }
                })
                .catch(() => {
                    if (!cancelled) setLoaded({ key, groups: null });
                });
        };
        try {
            unsubscribe = EventsOn(READY_EVENT, () => {
                if (waiting && !cancelled) setReady((count) => count + 1);
            });
        } catch {
            unsubscribe = undefined;
        }
        read();
        return () => {
            cancelled = true;
            unsubscribe?.();
        };
        // The titles are identified by their joined key; ready counts the ready events.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [key, ready, threshold]);
    return useMemo(() => {
        if (!loaded || loaded.key !== key || !loaded.groups || loaded.groups.length !== titles.length) return null;
        const aligned = new Array<number>(items.length);
        order.forEach((itemIndex, position) => { aligned[itemIndex] = loaded.groups![position]; });
        return aligned;
    }, [items.length, key, loaded, order, titles.length]);
}
