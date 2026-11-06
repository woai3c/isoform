// Who you know's saved view lives in Go (people_saved_view.go), beside the
// briefs, under the privacy stamp it was made under. This module only calls
// the bindings. The stamp is read before the reads and given back on save; a
// save under a stamp that has moved is refused, and a copy under another stamp
// is never served. Message text is stripped in Go before anything is written.
import {
    ForgetSavedPeopleView,
    GetSavedCopyStamp,
    GetSavedPeopleView,
    SavePeopleSavedView,
    type PeopleOverview,
    type PrincipalWithStatsDTO,
} from '@/lib/wails-shim-app';

export interface SavedPeopleView {
    overview: PeopleOverview;
    entries: PrincipalWithStatsDTO[];
}

/** The privacy stamp a read starts under. Empty when it cannot be read, and an
 *  empty stamp never saves. */
export async function peopleReadStamp(): Promise<string> {
    try {
        return (await GetSavedCopyStamp()) ?? '';
    } catch {
        return '';
    }
}

/** The saved directory, only while its stamp still holds (checked in Go). */
export async function readSavedPeopleView(): Promise<SavedPeopleView | null> {
    try {
        const saved = await GetSavedPeopleView();
        return saved ? { overview: saved.overview, entries: saved.entries } : null;
    } catch {
        return null;
    }
}

/** Saves a read made under stamp. Refused (and nothing written) if the stamp moved. */
export async function savePeopleView(stamp: string, overview: PeopleOverview, entries: PrincipalWithStatsDTO[]): Promise<void> {
    if (!stamp) return;
    try {
        await SavePeopleSavedView(stamp, overview, entries);
    } catch {
        // Refused or unavailable: the page simply has no saved copy to paint.
    }
}

/** Drops the saved directory: a write the copy would show as before, or a privacy change. */
export function clearSavedPeopleView() {
    try {
        void Promise.resolve(ForgetSavedPeopleView()).catch(() => undefined);
    } catch {
        // Nothing to drop.
    }
}
