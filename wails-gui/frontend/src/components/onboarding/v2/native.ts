import {
    PlaySetupFinish,
    RevealMainWindow,
    SetMenuBarMarkVisible,
    SetSetupWindowMode,
} from "@/lib/wails-shim-app";

// The native half of setup v2 (fixed window, menu bar mark, the flight to
// the menu bar; setup_window.go). Where these bindings fail (another
// platform, the browser fixture) each call resolves to null and setup
// carries on inside its window.

function quiet<T>(name: string, call: () => Promise<T>): Promise<T | null> {
    try {
        return Promise.resolve(call()).catch((error: unknown) => {
            console.warn(`[setup-v2] ${name} failed`, error);
            return null;
        });
    } catch (error) {
        console.warn(`[setup-v2] ${name} failed`, error);
        return Promise.resolve(null);
    }
}

const MARK_PULSED_KEY = "isoform.setup.v2.mark-pulsed";

/** Shows the menu bar mark when watching starts; it rings the first time only. */
export function setMenuBarMark(visible: boolean) {
    let pulse = visible;
    try {
        pulse = visible && localStorage.getItem(MARK_PULSED_KEY) !== "1";
        if (pulse) localStorage.setItem(MARK_PULSED_KEY, "1");
    } catch {
        /* Ring anyway. */
    }
    return quiet("SetMenuBarMarkVisible", () => SetMenuBarMarkVisible(visible, pulse));
}

export function setSetupWindowMode(on: boolean) {
    return quiet("SetSetupWindowMode", () => SetSetupWindowMode(on));
}

export interface SetupFinishResult {
    landed: "item" | "top-center";
}

export function playSetupFinish(req: {
    iconX: number;
    iconY: number;
    iconSize: number;
    callout: string;
    calloutHidden: string;
    reduceMotion: boolean;
}) {
    return quiet("PlaySetupFinish", () => PlaySetupFinish(req) as Promise<SetupFinishResult>);
}

export function revealMainWindow() {
    return quiet("RevealMainWindow", () => RevealMainWindow());
}
