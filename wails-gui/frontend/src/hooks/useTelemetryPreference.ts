import { useCallback, useEffect, useRef, useState } from "react";
import { GetTelemetryTier, SetTelemetryTier } from "../../wailsjs/go/main/App";
import { EventsOn } from "../../wailsjs/runtime/runtime";

/** The saved switch in onboarding and Settings. This does not authorize sends;
 * the product telemetry hooks and Go sender enforce their own consent gates. */
export function useTelemetryPreference() {
    const [enabled, setEnabled] = useState(true);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const revision = useRef(0);
    const pending = useRef(false);

    useEffect(() => {
        let active = true;
        const initialRevision = revision.current;
        const unsubscribe = EventsOn("telemetry:tier-changed", (tier: string) => {
            if (!active) return;
            revision.current += 1;
            setEnabled(tier !== "off");
            setLoading(false);
        });
        void GetTelemetryTier()
            .then((tier) => {
                if (active && revision.current === initialRevision) {
                    setEnabled(tier !== "off");
                }
            })
            // Keep the default visible if reading fails; an explicit toggle
            // can still save an opt-out through the existing binding.
            .catch(() => {})
            .finally(() => {
                if (active) setLoading(false);
            });
        return () => {
            active = false;
            unsubscribe?.();
        };
    }, []);

    const save = useCallback(async (next: boolean) => {
        if (pending.current) return;
        pending.current = true;
        const saveRevision = ++revision.current;
        setSaving(true);
        try {
            await SetTelemetryTier(next ? "detailed" : "off");
            // A more recent backend notification wins over a late response.
            if (revision.current === saveRevision) setEnabled(next);
        } finally {
            pending.current = false;
            setSaving(false);
        }
    }, []);

    return { enabled, loading, saving, setEnabled: save };
}
