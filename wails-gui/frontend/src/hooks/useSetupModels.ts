import { useCallback, useEffect, useRef, useState } from "react";
import { readSetupModels } from "@/lib/setup-model-adapter";
import { isMissingSetupBinding } from "@/lib/setup-api";
import type { ModelChoices, ModelDownload } from "@/lib/setup-model-types";
import type { ModelNetworkState } from "@/lib/setup-backend";
import { EventsOn } from "../../wailsjs/runtime/runtime";

export function useSetupModels() {
    const [choices, setChoices] = useState<ModelChoices | null>(null);
    const [downloads, setDownloads] = useState<ModelDownload[]>([]);
    const [network, setNetwork] = useState<ModelNetworkState | null>(null);
    const [unavailable, setUnavailable] = useState(false);
    const [error, setError] = useState(false);
    const live = useRef(false);
    const revision = useRef(0);
    const refresh = useCallback(async () => {
        const request = ++revision.current;
        try {
            const {
                choices: next,
                downloads: jobs,
                network: path,
            } = await readSetupModels();
            if (!live.current || request !== revision.current) return;
            setChoices(next);
            setDownloads(jobs ?? []);
            setNetwork(path);
            setError(false);
            setUnavailable(false);
        } catch (error) {
            if (!live.current || request !== revision.current) return;
            setUnavailable(isMissingSetupBinding(error));
            setError(!isMissingSetupBinding(error));
        }
    }, []);
    useEffect(() => {
        live.current = true;
        void refresh();
        const off = EventsOn("models:downloads", () => void refresh());
        return () => {
            live.current = false;
            revision.current++;
            off();
        };
    }, [refresh]);
    return { choices, downloads, network, unavailable, error, refresh };
}
