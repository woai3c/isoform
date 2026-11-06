import { useEffect, useMemo, useState } from 'react';
import { IdeaMeaningGroups } from '@/lib/wails-shim-app';

/** How often a pending answer is asked again, and for how long (about three minutes). */
const RETRY_MS = 1500;
const MAX_ATTEMPTS = 120;

/**
 * The meaning group of each item, from the app's embedder, aligned with items
 * (design review v1.0.8 W40, Y33; v1.0.9 R24). The page paints with its word
 * matcher at once. The app answers pending while it embeds titles in the
 * background, so this asks again until the groups arrive. Null when the app has
 * no embedder or the answer never comes; the page then keeps the word matcher.
 */
export function useIdeaMeanings<T>(
    items: readonly T[],
    titleOf: (item: T) => string,
    savedAtOf: (item: T) => number,
    /** The cosine threshold: how alike two titles must be to share a group. */
    threshold: number,
): number[] | null {
    const order = useMemo(
        () => items.map((_, index) => index).sort((a, b) => savedAtOf(items[b]) - savedAtOf(items[a]) || a - b),
        [items, savedAtOf],
    );
    const titles = useMemo(() => order.map((index) => titleOf(items[index])), [items, order, titleOf]);
    const key = useMemo(() => titles.join('\n'), [titles]);
    const [loaded, setLoaded] = useState<{ key: string; groups: number[] | null } | null>(null);
    // The retry count belongs to one set of titles; a new set starts at zero.
    const [retry, setRetry] = useState<{ key: string; attempt: number }>({ key, attempt: 0 });
    const attempt = retry.key === key ? retry.attempt : 0;
    useEffect(() => {
        if (titles.length === 0) return;
        let cancelled = false;
        let timer: number | undefined;
        // A missing binding (an older build, or a test double without it) falls back too.
        let pending: Promise<{ groups: number[] | null; pending: boolean } | null>;
        try {
            pending = IdeaMeaningGroups(titles, threshold);
        } catch {
            pending = Promise.resolve(null);
        }
        pending
            .then((answer) => {
                if (cancelled) return;
                if (answer?.groups) {
                    setLoaded({ key, groups: answer.groups });
                } else if (answer?.pending && attempt < MAX_ATTEMPTS) {
                    timer = window.setTimeout(() => setRetry({ key, attempt: attempt + 1 }), RETRY_MS);
                } else {
                    setLoaded({ key, groups: null });
                }
            })
            .catch(() => { if (!cancelled) setLoaded({ key, groups: null }); });
        return () => {
            cancelled = true;
            if (timer !== undefined) window.clearTimeout(timer);
        };
        // The titles are identified by their joined key; attempt counts the retries.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [key, attempt, threshold]);
    return useMemo(() => {
        if (!loaded || loaded.key !== key || !loaded.groups || loaded.groups.length !== titles.length) return null;
        const aligned = new Array<number>(items.length);
        order.forEach((itemIndex, position) => { aligned[itemIndex] = loaded.groups![position]; });
        return aligned;
    }, [items.length, key, loaded, order, titles.length]);
}
