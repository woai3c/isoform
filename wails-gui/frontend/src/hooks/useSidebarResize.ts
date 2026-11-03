import { useCallback, useEffect, useState } from 'react';

export const SIDEBAR_OPEN_WIDTH = 240;
export const SIDEBAR_COLLAPSED_WIDTH = 56;
export const SIDEBAR_COLLAPSE_BREAKPOINT = 1160;
const COLLAPSED_KEY = 'isoform.ui.sidebar.handCollapsed';

// Session storage is window-scoped UI state. An automatic collapse never
// overwrites the person's choice, including across a WebView reload.
export function useSidebarResize(_deps: { layoutRef: React.RefObject<HTMLDivElement | null> }) {
    const [handCollapsed, setHandCollapsed] = useState(() => {
        try { return sessionStorage.getItem(COLLAPSED_KEY) === 'true'; } catch { return false; }
    });
    const [narrow, setNarrow] = useState(() => window.innerWidth < SIDEBAR_COLLAPSE_BREAKPOINT);
    const [openedWhileNarrow, setOpenedWhileNarrow] = useState(false);
    const sidebarCollapsed = handCollapsed || (narrow && !openedWhileNarrow);
    const setSidebarCollapsed = useCallback<React.Dispatch<React.SetStateAction<boolean>>>((value) => {
        const next = typeof value === 'function' ? value(sidebarCollapsed) : value;
        setHandCollapsed(next);
        setOpenedWhileNarrow(!next && narrow);
        try { sessionStorage.setItem(COLLAPSED_KEY, String(next)); } catch { /* Storage may be unavailable. */ }
    }, [sidebarCollapsed, narrow]);

    useEffect(() => {
        const onResize = () => {
            setNarrow(window.innerWidth < SIDEBAR_COLLAPSE_BREAKPOINT);
            setOpenedWhileNarrow(false);
        };
        window.addEventListener('resize', onResize);
        return () => window.removeEventListener('resize', onResize);
    }, []);

    return { sidebarCollapsed, setSidebarCollapsed, sidebarWidth: SIDEBAR_OPEN_WIDTH };
}
