import { useEffect, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { AIReady } from "@/lib/wails-shim-app";

// useAIReady reports whether the user has a usable AI path configured — the
// single source of truth for the retirement banner's visibility. It is
// event-driven: the Go side emits "ai:config-changed" after every successful
// SaveAIProviderConfig, and this hook re-queries AIReady() in response. A
// slow polling fallback (every 10s) catches any state change that bypasses
// the event (e.g. a hand-edited ai-provider.json on disk).
//
// Returns tri-state: `null` while the first check is in flight (so the
// banner can defer its render decision until it knows the truth), `true`
// when an AI path is ready, `false` when it is not.
export function useAIReady(): boolean | null {
    const [ready, setReady] = useState<boolean | null>(null);

    useEffect(() => {
        let mounted = true;
        let timer: ReturnType<typeof setTimeout> | null = null;

        const check = () => {
            AIReady()
                .then((value) => {
                    if (mounted) setReady(Boolean(value));
                })
                .catch(() => {
                    // A binding failure should not flip the banner off — keep
                    // the last known value rather than silently hiding the
                    // notice on a transient IPC error.
                });
        };

        // Initial check + event-driven rechecks.
        check();
        const unsub = EventsOn("ai:config-changed", check);
        // Chat re-reads it when the window comes back to the front, so an AI
        // set up elsewhere clears the no-AI line without waiting for the poll.
        const onVisible = () => {
            if (document.visibilityState === "visible") check();
        };
        window.addEventListener("focus", check);
        document.addEventListener("visibilitychange", onVisible);

        // Polling fallback: re-check periodically so a config changed out of
        // band (manual file edit) still converges. 10s is infrequent enough
        // not to matter, frequent enough to feel responsive.
        const schedulePoll = () => {
            timer = setTimeout(() => {
                check();
                schedulePoll();
            }, 10_000);
        };
        schedulePoll();

        return () => {
            mounted = false;
            unsub();
            window.removeEventListener("focus", check);
            document.removeEventListener("visibilitychange", onVisible);
            if (timer) clearTimeout(timer);
        };
    }, []);

    return ready;
}
