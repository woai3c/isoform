import { useEffect, useState } from 'react';
import { clamp } from '@/lib/utils';

type ResizeAnchor = 'left' | 'right' | 'top' | 'bottom';

interface UseResizableOptions {
    anchor: ResizeAnchor;
    defaultSize: number;
    minSize: number;
    maxSize: number;
    storageKey: string;
    layoutRef: React.RefObject<HTMLDivElement | null>;
}

export function useResizable({
    anchor,
    defaultSize,
    minSize,
    maxSize,
    storageKey,
    layoutRef,
}: UseResizableOptions) {
    const [size, setSize] = useState(() => {
        try {
            const saved = localStorage.getItem(storageKey);
            const parsed = saved ? Number(saved) : NaN;
            if (!Number.isFinite(parsed)) return defaultSize;
            return clamp(parsed, minSize, maxSize);
        } catch {
            return defaultSize;
        }
    });
    const [isResizing, setIsResizing] = useState(false);

    // Persist size to localStorage
    useEffect(() => {
        try {
            localStorage.setItem(storageKey, String(size));
        } catch {
            // ignore
        }
    }, [size, storageKey]);

    // Mouse tracking for drag resize
    useEffect(() => {
        if (!isResizing) return;

        const isHorizontal = anchor === 'left' || anchor === 'right';
        const cursor = isHorizontal ? 'col-resize' : 'row-resize';

        const handleMouseMove = (event: MouseEvent) => {
            if (!layoutRef.current) return;
            const rect = layoutRef.current.getBoundingClientRect();

            let next: number;
            switch (anchor) {
                case 'left':
                    next = event.clientX - rect.left;
                    break;
                case 'right':
                    next = rect.right - event.clientX;
                    break;
                case 'top':
                    next = event.clientY - rect.top;
                    break;
                case 'bottom':
                    next = rect.bottom - event.clientY;
                    break;
            }

            setSize(clamp(next, minSize, maxSize));
        };

        const handleMouseUp = () => setIsResizing(false);

        document.addEventListener('mousemove', handleMouseMove);
        document.addEventListener('mouseup', handleMouseUp);
        document.body.style.userSelect = 'none';
        document.body.style.cursor = cursor;

        return () => {
            document.removeEventListener('mousemove', handleMouseMove);
            document.removeEventListener('mouseup', handleMouseUp);
            document.body.style.userSelect = '';
            document.body.style.cursor = '';
        };
    }, [isResizing, anchor, minSize, maxSize, layoutRef]);

    const startResize = () => setIsResizing(true);

    return { size, setSize, isResizing, startResize };
}
