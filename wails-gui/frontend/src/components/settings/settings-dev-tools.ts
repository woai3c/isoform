import { createContext, useContext } from "react";

// True while Developer tools are on (⌘⇧⌥D). Developer-only detail inside an
// ordinary Settings page (commands, raw tables, model IDs) shows only then.
export const SettingsDevToolsContext = createContext(false);

export function useSettingsDevTools(): boolean {
    return useContext(SettingsDevToolsContext);
}
