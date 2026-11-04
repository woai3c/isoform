import { createContext, useContext } from 'react';

/** True when developer tools are unlocked: technical details, raw ids and
 *  agent logs show only then (how-you-do-things review v1.0.0 W12, W18). */
export const DevToolsContext = createContext(false);

export function useDevTools(): boolean {
    return useContext(DevToolsContext);
}
