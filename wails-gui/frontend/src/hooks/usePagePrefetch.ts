import { useEffect, useRef } from 'react';
import { prefetchMainChat, prefetchToday } from '@/lib/page-queries';

/** Only warm the opposite page once, after the visible page has content. */
export function usePagePrefetch(page: string, ready: boolean) {
    const warmed = useRef(new Set<string>());
    useEffect(() => {
        if (!ready || !['today', 'chat'].includes(page) || warmed.current.has(page)) return;
        let idle: number | undefined;
        let timer: ReturnType<typeof setTimeout> | undefined;
        const warm = () => {
            warmed.current.add(page);
            void (page === 'today' ? prefetchMainChat() : prefetchToday()).catch(() => {});
        };
        const frame = requestAnimationFrame(() => {
            if (window.requestIdleCallback) idle = window.requestIdleCallback(warm, { timeout: 2000 });
            else timer = setTimeout(warm, 0);
        });
        return () => { cancelAnimationFrame(frame); if (idle !== undefined) window.cancelIdleCallback(idle); clearTimeout(timer); };
    }, [page, ready]);
}
