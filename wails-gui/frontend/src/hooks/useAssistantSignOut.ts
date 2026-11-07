import { useEffect, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { GetAssistantSignOut, type AssistantSignOutView } from "@/lib/wails-shim-app";

// The Go side emits "assistant-signout:changed" when a sign-out is confirmed
// or cleared (R75). The hook reads the state again on that event and on mount.
const SIGN_OUT_CHANGED_EVENT = "assistant-signout:changed";

/**
 * The chosen assistant whose sign-in is confirmed gone, or null. A failed read
 * shows nothing: an unknown state is never reported as a sign-out.
 */
export function useAssistantSignOut(): AssistantSignOutView | null {
    const [state, setState] = useState<AssistantSignOutView | null>(null);

    useEffect(() => {
        let mounted = true;
        const load = () => {
            GetAssistantSignOut()
                .then((view) => {
                    if (mounted) setState(view?.signedOut ? view : null);
                })
                .catch(() => {
                    // Keep the last known state rather than inventing one.
                });
        };
        load();
        const off = EventsOn(SIGN_OUT_CHANGED_EVENT, load);
        return () => {
            mounted = false;
            off();
        };
    }, []);

    return state;
}
