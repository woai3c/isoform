import { useEffect, useRef } from 'react';

const SCROLL_STEP = 60;
const GG_TIMEOUT = 500;

function isEditableElement(el: Element | null): boolean {
    if (!el) return false;
    const tag = el.tagName;
    if (tag === 'TEXTAREA' || tag === 'INPUT') return true;
    if ((el as HTMLElement).isContentEditable) return true;
    return false;
}

/**
 * Adds Vim-style keyboard navigation to a Radix ScrollArea.
 *
 * Keys (only active when no input element is focused):
 *   i          – focus composer (enter "insert mode")
 *   j/k        – scroll down/up by SCROLL_STEP
 *   Ctrl+D/U   – scroll down/up half page
 *   Ctrl+F/B   – scroll down/up full page
 *   G (Shift)  – scroll to bottom
 *   gg         – scroll to top
 *   Escape     – blur active element (exit "insert mode")
 */
export function useVimScroll(
    enabled: boolean,
    scrollAreaRef: React.RefObject<HTMLDivElement | null>,
    onInsert?: () => void,
) {
    const lastGTimeRef = useRef(0);

    useEffect(() => {
        if (!enabled) return;

        const getViewport = (): HTMLElement | null =>
            scrollAreaRef.current?.querySelector(
                '[data-radix-scroll-area-viewport]',
            ) as HTMLElement | null;

        const scrollBy = (delta: number) => {
            const vp = getViewport();
            if (vp) vp.scrollTop += delta;
        };

        const scrollTo = (top: number) => {
            const vp = getViewport();
            if (vp) vp.scrollTop = top;
        };

        const handleKeyDown = (e: KeyboardEvent) => {
            // Escape always works — blurs the active element
            if (e.key === 'Escape') {
                if (isEditableElement(document.activeElement)) {
                    (document.activeElement as HTMLElement).blur();
                    e.preventDefault();
                }
                return;
            }

            // Skip all vim keys while an editable element is focused
            if (isEditableElement(document.activeElement)) return;

            const vp = getViewport();
            if (!vp) return;

            // Ctrl-combos
            if (e.ctrlKey && !e.metaKey && !e.altKey) {
                switch (e.key) {
                    case 'd':
                        e.preventDefault();
                        scrollBy(vp.clientHeight / 2);
                        return;
                    case 'u':
                        e.preventDefault();
                        scrollBy(-vp.clientHeight / 2);
                        return;
                    case 'f':
                        e.preventDefault();
                        scrollBy(vp.clientHeight);
                        return;
                    case 'b':
                        e.preventDefault();
                        scrollBy(-vp.clientHeight);
                        return;
                }
            }

            // Plain keys (no modifier)
            if (e.ctrlKey || e.metaKey || e.altKey) return;

            switch (e.key) {
                case 'i':
                    if (onInsert) {
                        e.preventDefault();
                        onInsert();
                    }
                    return;
                case 'j':
                    e.preventDefault();
                    scrollBy(SCROLL_STEP);
                    return;
                case 'k':
                    e.preventDefault();
                    scrollBy(-SCROLL_STEP);
                    return;
                case 'G':
                    // Shift+G → scroll to bottom
                    e.preventDefault();
                    scrollTo(vp.scrollHeight);
                    return;
                case 'g':
                    // gg sequence: two g presses within GG_TIMEOUT
                    {
                        const now = Date.now();
                        if (now - lastGTimeRef.current < GG_TIMEOUT) {
                            e.preventDefault();
                            scrollTo(0);
                            lastGTimeRef.current = 0;
                        } else {
                            lastGTimeRef.current = now;
                        }
                    }
                    return;
            }
        };

        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [enabled, scrollAreaRef, onInsert]);
}
