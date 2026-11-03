/**
 * Three-dot cycling ellipsis (".", "..", "...") used by inline "running"
 * labels in tool group headers and similar in-progress indicators.
 *
 * Each dot cycles its opacity on a 1.2s loop with a 200ms stagger, so the
 * trailing dots appear to fill in and reset. Width is reserved for all
 * three dots so the surrounding text does not jitter as dots come and go.
 */
export function AnimatedEllipsis({ className = '' }: { className?: string }) {
    return (
        <span
            aria-hidden
            className={`inline-flex items-baseline tabular-nums select-none ${className}`}
        >
            <span
                style={{ animation: 'ellipsis-dot 1.2s steps(1, end) 0ms infinite' }}
            >
                .
            </span>
            <span
                style={{ animation: 'ellipsis-dot 1.2s steps(1, end) 200ms infinite' }}
            >
                .
            </span>
            <span
                style={{ animation: 'ellipsis-dot 1.2s steps(1, end) 400ms infinite' }}
            >
                .
            </span>
        </span>
    );
}
