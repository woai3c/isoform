const HOLD_SETTINGS_CHANGED = "isoform:hold-settings-changed";

export function notifyHoldSettingsChanged(): void {
    window.dispatchEvent(new Event(HOLD_SETTINGS_CHANGED));
}

export function onHoldSettingsChanged(refresh: () => void, includeFocus = true): () => void {
    window.addEventListener(HOLD_SETTINGS_CHANGED, refresh);
    if (includeFocus) window.addEventListener("focus", refresh);
    return () => {
        window.removeEventListener(HOLD_SETTINGS_CHANGED, refresh);
        if (includeFocus) window.removeEventListener("focus", refresh);
    };
}
