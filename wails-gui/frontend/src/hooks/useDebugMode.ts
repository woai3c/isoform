import { useCallback, useEffect, useState } from 'react';

const STORAGE_KEY = 'yansu.debug-mode-tools';
const EVENT_NAME = 'yansu:debug-mode-tools-changed';

function readStored(): boolean {
    try {
        return localStorage.getItem(STORAGE_KEY) === '1';
    } catch {
        return false;
    }
}

function writeStored(value: boolean): void {
    try {
        if (value) {
            localStorage.setItem(STORAGE_KEY, '1');
        } else {
            localStorage.removeItem(STORAGE_KEY);
        }
    } catch {
        // ignore quota / disabled storage; in-memory state still applies
    }
}

// useDebugMode toggles raw rendering of tool calls and their results in the
// chat stream. Lives behind Settings → Dev Tools → Debug. Persists in
// localStorage and broadcasts a window event so every mounted instance stays
// in sync without going through a React Context.
export function useDebugMode(): [boolean, (next: boolean) => void] {
    const [enabled, setEnabled] = useState<boolean>(readStored);

    useEffect(() => {
        const onChange = () => setEnabled(readStored());
        window.addEventListener(EVENT_NAME, onChange);
        // Cross-tab / cross-window updates would land via 'storage'; same-tab
        // updates need our custom event because 'storage' only fires for other
        // documents.
        window.addEventListener('storage', onChange);
        return () => {
            window.removeEventListener(EVENT_NAME, onChange);
            window.removeEventListener('storage', onChange);
        };
    }, []);

    const set = useCallback((next: boolean) => {
        writeStored(next);
        setEnabled(next);
        window.dispatchEvent(new Event(EVENT_NAME));
    }, []);

    return [enabled, set];
}
