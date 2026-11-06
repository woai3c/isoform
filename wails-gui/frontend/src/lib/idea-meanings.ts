import { useEffect, useMemo, useState } from 'react';
import { IdeaMeaningGroups } from '@/lib/wails-shim-app';

/**
 * The meaning group of each item, from the app's embedder, aligned with items
 * (design review v1.0.8 W40, Y33). Null until the groups arrive, or when the
 * app has no embedder; the page then groups by its word matcher.
 */
export function useIdeaMeanings<T>(
    items: readonly T[],
    titleOf: (item: T) => string,
    savedAtOf: (item: T) => number,
): number[] | null {
    const order = useMemo(
        () => items.map((_, index) => index).sort((a, b) => savedAtOf(items[b]) - savedAtOf(items[a]) || a - b),
        [items, savedAtOf],
    );
    const titles = useMemo(() => order.map((index) => titleOf(items[index])), [items, order, titleOf]);
    const key = useMemo(() => titles.join('\n'), [titles]);
    const [loaded, setLoaded] = useState<{ key: string; groups: number[] | null } | null>(null);
    useEffect(() => {
        if (titles.length === 0) return;
        let cancelled = false;
        // A missing binding (an older build, or a test double without it) falls back too.
        let pending: Promise<number[] | null>;
        try {
            pending = IdeaMeaningGroups(titles);
        } catch {
            pending = Promise.resolve(null);
        }
        pending
            .then((groups) => { if (!cancelled) setLoaded({ key, groups: groups ?? null }); })
            .catch(() => { if (!cancelled) setLoaded({ key, groups: null }); });
        return () => { cancelled = true; };
        // The titles are identified by their joined key.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [key]);
    return useMemo(() => {
        if (!loaded || loaded.key !== key || !loaded.groups || loaded.groups.length !== titles.length) return null;
        const aligned = new Array<number>(items.length);
        order.forEach((itemIndex, position) => { aligned[itemIndex] = loaded.groups![position]; });
        return aligned;
    }, [items.length, key, loaded, order, titles.length]);
}
