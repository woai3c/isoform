import { useLayoutEffect, useRef, type ReactNode } from 'react';

// Animated expand/collapse wrapper. Uses the grid-template-rows 0fr↔1fr
// idiom (same technique as processing-block / composer-suggestions) so we
// can smoothly animate to `auto` height without measuring the child. The
// inner div owns overflow:hidden so the child's natural layout is untouched
// while collapsed. Content stays mounted during collapse to preserve state
// and keep the open animation starting from the correct measured height.
// While closed the wrapper is inert, so keyboard focus skips the hidden
// controls just as assistive tech does (the attribute is set from a ref:
// React 18's types only know `inert` as experimental).
export function CollapsePanel({
    open,
    children,
    innerClassName,
}: {
    open: boolean;
    children: ReactNode;
    innerClassName?: string;
}) {
    const ref = useRef<HTMLDivElement>(null);
    useLayoutEffect(() => {
        ref.current?.toggleAttribute('inert', !open);
    }, [open]);
    return (
        <div
            ref={ref}
            aria-hidden={!open}
            className="grid transition-[grid-template-rows,opacity] duration-[var(--motion-duration-slow)] ease-[var(--motion-ease-in-out)]"
            style={{
                gridTemplateRows: open ? '1fr' : '0fr',
                opacity: open ? 1 : 0,
            }}
        >
            <div className={`min-h-0 overflow-hidden${innerClassName ? ' ' + innerClassName : ''}`}>
                {children}
            </div>
        </div>
    );
}
