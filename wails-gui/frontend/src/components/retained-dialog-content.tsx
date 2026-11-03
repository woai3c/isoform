import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ComponentProps } from 'react';
import { createPortal } from 'react-dom';
import { DialogContent } from '@/components/ui/dialog';
import { markPageContent, type PageDataSource } from '@/lib/page-switch-timing';

/** Keep a page's dialog content mounted across closes: once opened, its data,
 * scroll position and filters are exactly where the user left them on the
 * next open. (`retain` still covers an Activity jump from a page that is
 * open right now.) The portaled children live in a detached container while
 * closed, so no modal, focus trap, or overlay stays active. Scroll offsets
 * are remembered per element: a detached subtree loses them on reattach. */
export function RetainedDialogContent({
    open,
    retain,
    children,
    page,
    ...props
}: ComponentProps<typeof DialogContent> & { open: boolean; retain: boolean; page?: string }) {
    const [container] = useState(() => {
        const element = document.createElement('div');
        element.style.display = 'contents';
        return element;
    });
    const [visited, setVisited] = useState(open);
    if (open && !visited) setVisited(true);
    const attach = useCallback((host: HTMLDivElement | null) => {
        if (host) host.appendChild(container);
    }, [container]);

    // Capture-phase: descendant scrolls (Radix viewports, plain overflow
    // divs) reach here even though scroll does not bubble.
    const offsets = useRef(new Map<Element, { top: number; left: number }>());
    useEffect(() => {
        const onScroll = (event: Event) => {
            const target = event.target as HTMLElement | null;
            if (target && typeof target.scrollTop === 'number') {
                offsets.current.set(target, { top: target.scrollTop, left: target.scrollLeft });
            }
        };
        container.addEventListener('scroll', onScroll, true);
        return () => container.removeEventListener('scroll', onScroll, true);
    }, [container]);

    // Closing detaches the subtree; snapshot whatever is scrolled before the
    // browser forgets it (a detach zeroes offsets in some engines).
    const snapshot = useCallback(() => {
        container.querySelectorAll<HTMLElement>('*').forEach((element) => {
            if (element.scrollTop > 0 || element.scrollLeft > 0) {
                offsets.current.set(element, { top: element.scrollTop, left: element.scrollLeft });
            }
        });
    }, [container]);

    // Reopen: the same nodes come back with their state; a detached subtree
    // loses its scroll offsets on reattach, so restore them after the commit
    // has painted (the second frame, when layout exists again).
    useLayoutEffect(() => {
        if (!open) {
            snapshot();
            return;
        }
        if (offsets.current.size === 0) return;
        const restore = () => {
            for (const [element, offset] of offsets.current) {
                element.scrollTop = offset.top;
                element.scrollLeft = offset.left;
            }
        };
        restore();
        const frame = requestAnimationFrame(() => requestAnimationFrame(restore));
        return () => cancelAnimationFrame(frame);
    }, [open, snapshot]);

    // A revisit after the first open paints retained content at once; the
    // page's own mark covers the first open's read.
    const openedBefore = useRef(false);
    useEffect(() => {
        if (open && openedBefore.current && page) markPageContent(page, 'cache' satisfies PageDataSource);
        if (open) openedBefore.current = true;
    }, [open, page]);

    return (
        <>
            <DialogContent {...props}>
                <div ref={attach} style={{ display: 'contents' }} />
            </DialogContent>
            {(open || retain || visited) && createPortal(children, container)}
        </>
    );
}
