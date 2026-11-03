import { lazy, useEffect } from 'react';

// Activity and Settings are the two heaviest pages (the activity timeline
// and every settings section). They split out of the main bundle and warm
// ahead of first use: sidebar hover (sidebar.tsx) and an idle warm once the
// landing page has content, so opening them stays synchronous to the eye.
export const loadActivityPage = () => import('@/components/activity-page');
export const loadSettingsPage = () => import('@/components/settings-page');

// The terminal panel carries xterm; it is hidden until a terminal is opened,
// so it loads after the first paint instead of with the launch bundle.
export const loadTerminalPanel = () => import('@/components/terminal-panel');
export const LazyTerminalPanel = lazy(async () => ({ default: (await loadTerminalPanel()).TerminalPanel }));

export const LazyActivityPage = lazy(async () => ({ default: (await loadActivityPage()).ActivityPage }));
export const LazySettingsPage = lazy(async () => ({ default: (await loadSettingsPage()).SettingsPage }));

let warmed = false;
/** Loads both page chunks once; safe to call on every hover. */
export function warmLazyPages(): void {
    if (warmed) return;
    warmed = true;
    void loadActivityPage().catch(() => { warmed = false; });
    void loadSettingsPage().catch(() => { warmed = false; });
    void loadTerminalPanel().catch(() => { warmed = false; });
}

/** Idle warm after the landing page has content (same cadence as the data
 *  prefetch in usePagePrefetch). */
export function useLazyPagesIdleWarm(ready: boolean): void {
    useEffect(() => {
        if (!ready) return;
        let idle: number | undefined;
        let timer: ReturnType<typeof setTimeout> | undefined;
        const warm = () => warmLazyPages();
        const frame = requestAnimationFrame(() => {
            if (window.requestIdleCallback) idle = window.requestIdleCallback(warm, { timeout: 3000 });
            else timer = setTimeout(warm, 0);
        });
        return () => { cancelAnimationFrame(frame); if (idle !== undefined) window.cancelIdleCallback(idle); clearTimeout(timer); };
    }, [ready]);
}
