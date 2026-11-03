import { useCallback, useEffect, useState } from 'react';

const STORAGE_KEY = 'yansu.show-tool-steps';
const MIGRATED_KEY = 'yansu.show-tool-steps-migrated';
const EVENT_NAME = 'yansu:show-tool-steps-changed';

function readStored(): boolean {
    try {
        const stored = localStorage.getItem(STORAGE_KEY);
        if (localStorage.getItem(MIGRATED_KEY) === '1') return stored === '1';
        // The previous build removed this key for off, so absence cannot
        // distinguish an explicit off from never configured. Keep it off
        // even when legacy debug mode is on; only a stored on is unambiguous.
        const enabled = stored === '1';
        try {
            localStorage.setItem(MIGRATED_KEY, '1');
        } catch {
            // A full or read-only store must not discard a readable choice.
        }
        return enabled;
    } catch {
        return false;
    }
}

function writeStored(value: boolean): void {
    try {
        localStorage.setItem(STORAGE_KEY, value ? '1' : '0');
        localStorage.setItem(MIGRATED_KEY, '1');
    } catch {
        // ignore quota / disabled storage; in-memory state still applies
    }
}

// The General preference for inspecting tool inputs and results. Keep it
// independent of debug mode, with the same persisted, window-synchronized
// behavior as the neighboring presentation preferences.
export function useShowToolSteps(): [boolean, (next: boolean) => void] {
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
