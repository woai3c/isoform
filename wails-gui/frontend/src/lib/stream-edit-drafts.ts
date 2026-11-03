import { useSyncExternalStore } from 'react';
import { onPrivacyReset } from './privacy-reset';

// Unsent composer edits inside chat rows, kept by message id above the
// windowed list so scrolling a row out of the window (or any re-mount) no
// longer discards the draft the way row-local state did. Presence of an
// entry means the row's editor is open with this text. Like every render-tier
// cache, the whole map drops on Forget, protect-app, Closed Eyes and
// sign-out (lib/privacy-reset.ts).
const drafts = new Map<string, string>();
const listeners = new Set<() => void>();

function notify(): void {
    for (const listener of listeners) listener();
}

export function getStreamEditDraft(itemId: string): string | null {
    return drafts.get(itemId) ?? null;
}

export function setStreamEditDraft(itemId: string, text: string): void {
    if (drafts.get(itemId) === text) return;
    drafts.set(itemId, text);
    notify();
}

export function clearStreamEditDraft(itemId: string): void {
    if (!drafts.delete(itemId)) return;
    notify();
}

export function subscribeStreamEditDrafts(listener: () => void): () => void {
    listeners.add(listener);
    return () => { listeners.delete(listener); };
}

onPrivacyReset(() => {
    if (drafts.size === 0) return;
    drafts.clear();
    notify();
});

/** The row's open editor text, or null when it is not being edited. */
export function useStreamEditDraft(itemId: string): string | null {
    return useSyncExternalStore(
        subscribeStreamEditDrafts,
        () => getStreamEditDraft(itemId),
        () => getStreamEditDraft(itemId),
    );
}
