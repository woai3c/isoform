import { useEffect } from 'react';
import { startIdlePrefetch } from '@/lib/idle-prefetch';

/** Starts the one-time idle prefetch walk once Today's first paint lands. */
export function useIdlePagePrefetch(ready: boolean) {
    useEffect(() => {
        if (!ready) return;
        startIdlePrefetch();
    }, [ready]);
}
