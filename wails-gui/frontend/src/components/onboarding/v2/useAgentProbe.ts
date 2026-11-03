import { useCallback, useEffect, useRef, useState } from "react";
import {
    GetAgentProbe,
    SetSetupWelcomeVisible,
} from "@/lib/wails-shim-app";
import { EventsOn } from "../../../../wailsjs/runtime/runtime";
import type { SetupAgentProbe } from "@/lib/setup-backend";

const CACHE_KEY = "isoform.setup.agent-probe.v1";

function cachedProbe(): SetupAgentProbe | null {
    try {
        const value = JSON.parse(localStorage.getItem(CACHE_KEY) ?? "null");
        return Array.isArray(value?.agents) ? value : null;
    } catch {
        return null;
    }
}

/**
 * A brand-new install waits at most this long for the launch check before
 * screen 2 shows its cards (handoff §4, screen 2: no checking state).
 */
export const AGENT_PROBE_WAIT_MS = 1000;

/**
 * The assistant check that runs at launch, as screen 2 reads it: the last
 * snapshot at once, then updates while the screen is open (the backend
 * re-checks every 2 s, which picks up a sign-in finished in Terminal).
 */
export function useAgentProbe(enabled: boolean) {
    const [probe, setProbe] = useState<SetupAgentProbe | null>(() =>
        enabled ? cachedProbe() : null,
    );
    const [waitOver, setWaitOver] = useState(() => probe !== null);
    const live = useRef(false);

    const refresh = useCallback(async () => {
        try {
            const next = await GetAgentProbe();
            if (!live.current) return;
            setProbe(next);
            setWaitOver(true);
            try {
                localStorage.setItem(CACHE_KEY, JSON.stringify(next));
            } catch {
                /* Private storage may be unavailable. */
            }
        } catch {
            /* The snapshot stays usable. */
        }
    }, []);

    useEffect(() => {
        if (!enabled) return;
        live.current = true;
        const timer = setTimeout(() => setWaitOver(true), AGENT_PROBE_WAIT_MS);
        void SetSetupWelcomeVisible(true).catch(() => {});
        void refresh();
        const off = EventsOn("setup:agents-updated", () => void refresh());
        const offAccounts = EventsOn("model-accounts:updated", () => void refresh());
        return () => {
            live.current = false;
            clearTimeout(timer);
            void SetSetupWelcomeVisible(false).catch(() => {});
            off();
            offAccounts();
        };
    }, [enabled, refresh]);

    return { probe, ready: probe !== null || waitOver, refresh };
}
