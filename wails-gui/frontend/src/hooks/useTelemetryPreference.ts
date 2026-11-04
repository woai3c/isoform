import { useCallback, useEffect, useRef, useState } from "react";
import { GetTelemetryTier, SetTelemetryTier } from "../../wailsjs/go/main/App";
import { EventsOn } from "../../wailsjs/runtime/runtime";

/** The saved switch in onboarding and Settings. This does not authorize sends;
 * the product telemetry hooks and Go sender enforce their own consent gates. */
export function useTelemetryPreference() {
    const [enabled, setEnabled] = useState(true);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [readFailed, setReadFailed] = useState(false);
    const [readCount, setReadCount] = useState(0);
    const revision = useRef(0);
    const pending = useRef(false);

    useEffect(() => {
        let active = true;
        const initialRevision = revision.current;
        const unsubscribe = EventsOn("telemetry:tier-changed", (tier: string) => {
            if (!active) return;
            revision.current += 1;
            setEnabled(tier !== "off");
            setReadFailed(false);
            setLoading(false);
        });
        void GetTelemetryTier()
            .then((tier) => {
                if (active && revision.current === initialRevision) {
                    setEnabled(tier !== "off");
                    setReadFailed(false);
                }
            })
            // Never show the default as the truth when the read fails.
            .catch(() => {
                if (active && revision.current === initialRevision) setReadFailed(true);
            })
            .finally(() => {
                if (active) setLoading(false);
            });
        return () => {
            active = false;
            unsubscribe?.();
        };
    }, [readCount]);

    const read = useCallback(() => {
        setLoading(true);
        setReadFailed(false);
        setReadCount((n) => n + 1);
    }, []);

    const save = useCallback(async (next: boolean) => {
        if (pending.current) return;
        pending.current = true;
        const saveRevision = ++revision.current;
        setSaving(true);
        try {
            await SetTelemetryTier(next ? "detailed" : "off");
            // A more recent backend notification wins over a late response.
            if (revision.current === saveRevision) {
                setEnabled(next);
                setReadFailed(false);
            }
        } finally {
            pending.current = false;
            setSaving(false);
        }
    }, []);

    return { enabled, loading, saving, readFailed, read, setEnabled: save };
}
