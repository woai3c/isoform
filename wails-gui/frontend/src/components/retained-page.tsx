import { useEffect, useRef, useState, type ReactNode } from 'react';
import { markPageContent } from '@/lib/page-switch-timing';
import { RetainedPageActiveContext } from '@/lib/retained-page-active';

/** One instance per page, kept in the main column after its first visit. */
export function RetainedPage({ active, page, children }: { active: boolean; page: string; children: ReactNode }) {
    const [visited, setVisited] = useState(active);
    if (active && !visited) setVisited(true);
    // A revisit (the page was hidden after its first visit) paints the
    // retained content at once; the first visit's mark comes from the page's
    // own reads.
    const wasActive = useRef(active);
    const hiddenAfterVisit = useRef(false);
    useEffect(() => {
        if (active && !wasActive.current && hiddenAfterVisit.current) markPageContent(page, 'cache');
        if (!active && visited) hiddenAfterVisit.current = true;
        wasActive.current = active;
    }, [active, visited, page]);
    if (!active && !visited) return null;
    return (
        <RetainedPageActiveContext.Provider value={active}>
            <div data-retained-page={page} className="flex flex-1 min-h-0 flex-col" style={{ display: active ? undefined : 'none' }}>{children}</div>
        </RetainedPageActiveContext.Provider>
    );
}
