import type { SVGProps } from 'react';

/**
 * Deliverable icon — a parcel / outbox mark used in the sidebar Library nav for
 * the unified Deliverables hub. Matches HandoffIcon's stroke style. Inherits
 * color via currentColor.
 */
export function DeliverableIcon({ size = 20, className, ...props }: SVGProps<SVGSVGElement> & { size?: number }) {
    return (
        <svg
            width={size}
            height={size}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth={1.75}
            strokeLinecap="round"
            strokeLinejoin="round"
            className={className}
            {...props}
        >
            <path d="M3 9 5 4h14l2 5" />
            <path d="M3 9v9a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V9z" />
            <path d="M3 9h6l1.5 2.5h3L15 9h6" />
        </svg>
    );
}
