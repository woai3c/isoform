// Who you know's saved view: a display-only copy of the last directory read,
// so the page paints at once on the next launch while the real read runs
// behind it. It never holds message text and never stands in for a read: a
// privacy withdrawal or a failed parse drops it.
import type { PeopleOverview, PrincipalWithStatsDTO } from '@/lib/wails-shim-app';

const KEY = 'isoform.people.saved-view';
const SCHEMA = 'people-view:1';
const MAX_CHARS = 1_000_000;

export interface SavedPeopleView {
    overview: PeopleOverview;
    entries: PrincipalWithStatsDTO[];
}

function storage(): Storage | null {
    try {
        return typeof window === 'undefined' ? null : window.localStorage;
    } catch {
        return null;
    }
}

export function readSavedPeopleView(): SavedPeopleView | null {
    const store = storage();
    if (!store) return null;
    try {
        const raw = store.getItem(KEY);
        if (!raw) return null;
        const parsed = JSON.parse(raw) as { schema?: string; overview?: PeopleOverview; entries?: PrincipalWithStatsDTO[] };
        if (parsed?.schema !== SCHEMA || !parsed.overview || !Array.isArray(parsed.overview.open) || !Array.isArray(parsed.overview.everyone) || !Array.isArray(parsed.entries)) {
            store.removeItem(KEY);
            return null;
        }
        return { overview: parsed.overview, entries: parsed.entries };
    } catch {
        store.removeItem(KEY);
        return null;
    }
}

export function savePeopleView(overview: PeopleOverview, entries: PrincipalWithStatsDTO[]) {
    const store = storage();
    if (!store) return;
    const withoutText = entries.map((entry) => (entry.stats ? { ...entry, stats: { ...entry.stats, recentMessages: [] } } : entry));
    try {
        const raw = JSON.stringify({ schema: SCHEMA, overview, entries: withoutText });
        if (raw.length > MAX_CHARS) store.removeItem(KEY);
        else store.setItem(KEY, raw);
    } catch {
        // A full or unavailable store only costs the instant paint.
    }
}

export function clearSavedPeopleView() {
    try {
        storage()?.removeItem(KEY);
    } catch {
        // Nothing saved to drop.
    }
}
