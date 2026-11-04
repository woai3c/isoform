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
    /** The playbooks' read failed: the last list stays on screen (W21). */
    loadFailed: boolean;
    /** The plans (schedules) read failed: no when-line rather than an untrue one. */
    schedulesFailed: boolean;
    /** True once a read of the playbooks has succeeded. */
    loaded: boolean;
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
    const [loadFailed, setLoadFailed] = useState(false);
    const [schedulesFailed, setSchedulesFailed] = useState(false);
    const [loaded, setLoaded] = useState(false);

    // A failed read keeps what is on screen (W21).
    const reload = useCallback(() => {
        ListSOPs()
            .then((rows) => {
                setSops(Array.isArray(rows) ? rows : []);
                setLoadFailed(false);
                setLoaded(true);
            })
            .catch(() => setLoadFailed(true));
        GetPlaybooksState()
            .then((state) => {
                setPlaybooks(state ?? null);
                setSchedulesFailed(false);
            })
            .catch(() => setSchedulesFailed(true));
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

    return { sops, playbooks, authorStatus, loadFailed, schedulesFailed, loaded, reload };
}
