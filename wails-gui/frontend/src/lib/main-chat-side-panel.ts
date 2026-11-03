import { useSyncExternalStore } from "react";

// Chat's own 420 pt panel for what a click opens without leaving Chat (K4):
// a running task's thread, or a draft reply card with Send and Edit.
export type MainChatSidePanel =
    | { kind: "task"; id: string }
    | { kind: "draft"; text: string; send: (text: string) => unknown };

let current: MainChatSidePanel | null = null;
const listeners = new Set<() => void>();

function publish(next: MainChatSidePanel | null) {
    current = next;
    for (const listener of listeners) listener();
}

export function openMainChatTask(id: string) {
    publish({ kind: "task", id });
}

export function openMainChatReplyDraft(text: string, send: (text: string) => unknown) {
    publish({ kind: "draft", text, send });
}

export function closeMainChatSidePanel() {
    if (current) publish(null);
}

export function useMainChatSidePanel(): MainChatSidePanel | null {
    return useSyncExternalStore(
        (listener) => {
            listeners.add(listener);
            return () => listeners.delete(listener);
        },
        () => current,
        () => current,
    );
}
