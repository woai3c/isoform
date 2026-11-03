import { useCallback, useEffect, useState } from 'react';
import {
    GetPlaybooksState,
    GetSOPAuthorStatus,
    ListSOPs,
    type PlaybooksState,
    type SopAuthorStatus,
    type SopEntry,
} from '@/lib/wails-shim-app';
import { EventsOn } from '../../../wailsjs/runtime/runtime';

export type PlaybookLibrary = {
    sops: SopEntry[];
    playbooks: PlaybooksState | null;
    authorStatus: SopAuthorStatus | null;
    /** Re-reads the playbooks and their plans. */
    reload: () => void;
};

/**
 * The playbooks Isoform has learned, with their plans and the live author
 * status. Refreshes on `sop:updated` and `playbook:updated`, as the Library
 * playbooks list did.
 */
export function usePlaybookLibrary(): PlaybookLibrary {
    const [sops, setSops] = useState<SopEntry[]>([]);
    const [playbooks, setPlaybooks] = useState<PlaybooksState | null>(null);
    const [authorStatus, setAuthorStatus] = useState<SopAuthorStatus | null>(null);

    const reload = useCallback(() => {
        ListSOPs()
            .then((rows) => setSops(Array.isArray(rows) ? rows : []))
            .catch(() => setSops([]));
        GetPlaybooksState()
            .then((state) => setPlaybooks(state ?? null))
            .catch(() => setPlaybooks(null));
    }, []);

    useEffect(() => {
        reload();
        GetSOPAuthorStatus()
            .then((status) => setAuthorStatus(status ?? null))
            .catch(() => {});
        const unsubSops = EventsOn('sop:updated', () => reload());
        const unsubPlaybooks = EventsOn('playbook:updated', () => reload());
        const unsubAuthor = EventsOn('sop:author-status', (status: SopAuthorStatus) => setAuthorStatus(status ?? null));
        return () => {
            unsubSops?.();
            unsubPlaybooks?.();
            unsubAuthor?.();
        };
    }, [reload]);

    return { sops, playbooks, authorStatus, reload };
}
