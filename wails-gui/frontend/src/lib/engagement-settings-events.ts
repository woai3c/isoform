import { EventsOn } from '../../wailsjs/runtime/runtime';
import { GetEngagementSettings, type EngagementKindView } from './wails-shim-app';

type EngagementSettingsChanged = { kinds: Pick<EngagementKindView, 'kind' | 'enabled'>[] };
type Listener = (view: EngagementSettingsChanged) => void;
const listeners = new Set<Listener>();
let unsubscribe: (() => void) | null = null;
let latest: EngagementSettingsChanged | null = null;
let version = 0;
let subscriptionEpoch = 0;

/** Keep mounted cards in sync with preferences without reloading chat history. */
export function publishEngagementSettings(view: EngagementSettingsChanged) {
    latest = view;
    version += 1;
    for (const listener of listeners) listener(view);
}

export function onEngagementSettingsChanged(listener: Listener) {
    listeners.add(listener);
    if (latest) listener(latest);
    if (!unsubscribe) {
        unsubscribe = EventsOn('engagement:settings-changed', (view: EngagementSettingsChanged) => {
            if (view && Array.isArray(view.kinds)) publishEngagementSettings(view);
        });
        // Cached chat items outlive their mounted cards. Replay local saves,
        // then read once to catch phone changes made while no cards listened.
        const epoch = ++subscriptionEpoch;
        const readVersion = version;
        void (async () => {
            try {
                const view = await GetEngagementSettings();
                if (view?.available && epoch === subscriptionEpoch && readVersion === version) {
                    publishEngagementSettings(view);
                }
            } catch {
                // Keep the saved card and last confirmed preference usable.
            }
        })();
    }
    return () => {
        listeners.delete(listener);
        if (listeners.size === 0) {
            unsubscribe?.();
            unsubscribe = null;
            subscriptionEpoch += 1;
        }
    };
}
