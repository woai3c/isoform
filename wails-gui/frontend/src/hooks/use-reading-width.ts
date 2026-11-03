import { useSyncExternalStore } from "react";

const key = "yansu.chat-wide-mode";
const event = "isoform:reading-width";
function read() {
    try {
        return localStorage.getItem(key) !== "false";
    } catch {
        return true;
    }
}
function subscribe(listener: () => void) {
    window.addEventListener(event, listener);
    window.addEventListener("storage", listener);
    return () => {
        window.removeEventListener(event, listener);
        window.removeEventListener("storage", listener);
    };
}
export function useReadingWidth() {
    const full = useSyncExternalStore(subscribe, read);
    return [
        full,
        (value: boolean) => {
            try {
                localStorage.setItem(key, String(value));
            } catch {
                return;
            }
            window.dispatchEvent(new Event(event));
        },
    ] as const;
}
