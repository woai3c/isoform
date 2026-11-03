import { useCallback, useState } from 'react';

export interface SnapshotFallback {
    /** src while it can be shown; null once it failed to load. */
    visibleSrc: string | null;
    /** True when src failed to load and a placeholder should show instead. */
    missing: boolean;
    /** Pass to the image's onError. */
    onError: () => void;
}

/**
 * Decides whether an activity screenshot URL is shown or replaced by a
 * placeholder. A URL that fails to load (file never written, removed by
 * storage rotation, or not written yet) is hidden. The failure is forgotten
 * whenever src changes, so moving to another frame and back tries again: a
 * frame whose masked file lands late shows up on the next visit.
 */
export function useSnapshotFallback(src: string | null | undefined): SnapshotFallback {
    const current = src ?? null;
    const [failedSrc, setFailedSrc] = useState<string | null>(null);
    const [trackedSrc, setTrackedSrc] = useState<string | null>(current);
    if (trackedSrc !== current) {
        setTrackedSrc(current);
        setFailedSrc(null);
    }

    const onError = useCallback(() => {
        if (current) setFailedSrc(current);
    }, [current]);

    const missing = current !== null && failedSrc === current;
    return { visibleSrc: missing ? null : current, missing, onError };
}
