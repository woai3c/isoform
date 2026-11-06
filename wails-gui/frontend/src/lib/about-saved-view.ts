// About you's saved beliefs live in Go (about_you_saved.go), under the privacy
// stamp they were made under. This module calls the bindings and throttles
// saves: a copy older than a few minutes still paints on the first open while
// the reads run, and a privacy change drops it through the stamp.
import { ForgetSavedAboutYou, GetSavedAboutYou, SaveAboutYouSavedView, type AboutYouView } from '@/lib/wails-shim-app';

/** Saves are throttled to one per this long; a saved copy is only a first paint. */
export const ABOUT_YOU_SAVE_EVERY_MS = 5 * 60_000;
let lastSavedAt = 0;

export interface SavedAboutYou {
    about: AboutYouView;
    entries: unknown[];
}

/** The saved beliefs, only while their stamp still holds (checked in Go). */
export async function readSavedAboutYou(): Promise<SavedAboutYou | null> {
    try {
        const saved = await GetSavedAboutYou();
        return saved ? { about: saved.about, entries: saved.entries } : null;
    } catch {
        return null;
    }
}

/** Saves a read made under stamp, at most once per ABOUT_YOU_SAVE_EVERY_MS. */
export async function saveAboutYou(stamp: string, about: AboutYouView, entries: unknown[]): Promise<void> {
    if (!stamp || Date.now() - lastSavedAt < ABOUT_YOU_SAVE_EVERY_MS) return;
    lastSavedAt = Date.now();
    try {
        await SaveAboutYouSavedView(stamp, about, entries);
    } catch {
        lastSavedAt = 0;
    }
}

/** Drops the saved beliefs after a withdrawal or a write the copy would show as before. */
export function forgetSavedAboutYou() {
    lastSavedAt = 0;
    try {
        void Promise.resolve(ForgetSavedAboutYou()).catch(() => undefined);
    } catch {
        // Nothing to drop.
    }
}

/** Test seam: forgets the last save so the next save runs at once. */
export function resetAboutYouSaveThrottle() {
    lastSavedAt = 0;
}
