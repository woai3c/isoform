import { useCallback, useEffect, useRef, useState } from "react";
import { GetAboutInfo, type AboutInfo } from "@/lib/wails-shim-app";
import { EventsOn } from "../../wailsjs/runtime/runtime";

export function useAboutInfo() {
    const [info, setInfo] = useState<AboutInfo | null>(null);
    const [failed, setFailed] = useState(false);
    const revision = useRef(0);
    const eventSequence = useRef(0);
    const latestUpdate = useRef<AboutInfo["update"] | null>(null);
    const refresh = useCallback(async () => {
        const request = ++revision.current;
        const startedAt = eventSequence.current;
        try {
            const next = await GetAboutInfo();
            if (request === revision.current) {
                // Keep fresh policy/device metadata even while progress arrives,
                // while preserving any newer updater event over this snapshot.
                setInfo(
                    eventSequence.current > startedAt && latestUpdate.current
                        ? { ...next, update: latestUpdate.current }
                        : next,
                );
                setFailed(false);
            }
        } catch {
            if (request === revision.current) setFailed(true);
        }
    }, []);
    useEffect(() => {
        void refresh();
        const off = EventsOn("update:status", (update: AboutInfo["update"]) => {
            ++eventSequence.current;
            latestUpdate.current = update;
            setInfo((previous) =>
                previous ? { ...previous, update } : previous,
            );
            // Metadata is refreshed only at lifecycle boundaries, not every chunk.
            if (update.status !== "downloading") void refresh();
        });
        return () => {
            ++revision.current;
            off();
        };
    }, [refresh]);
    return { info, failed, refresh };
}
