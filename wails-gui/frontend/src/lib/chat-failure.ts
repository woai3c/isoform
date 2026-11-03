import { useSyncExternalStore } from 'react';

// K3: automatic loads and saves get one quiet retry before any line shows.
export async function quietRetry<T>(run: () => Promise<T>, retryable: (error: unknown) => boolean = () => true): Promise<T> {
    try {
        return await run();
    } catch (error) {
        if (!retryable(error)) throw error;
        return await run();
    }
}

// K3: a Chat open that failed, said once in place on Chat (never a toast).
export type ChatPlaceFailure = { key: 'failure.loadChat' | 'failure.openIt'; retry?: () => unknown };

let placeFailure: ChatPlaceFailure | null = null;
const placeListeners = new Set<() => void>();

export function reportChatPlaceFailure(next: ChatPlaceFailure | null) {
    placeFailure = next;
    for (const listener of placeListeners) listener();
}

export function useChatPlaceFailure(): ChatPlaceFailure | null {
    return useSyncExternalStore(
        (listener) => {
            placeListeners.add(listener);
            return () => placeListeners.delete(listener);
        },
        () => placeFailure,
        () => placeFailure,
    );
}
