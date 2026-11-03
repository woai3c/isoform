import type { ExecutorType } from '@/lib/stream-helpers';

interface TypingIndicatorProps {
    executorType?: ExecutorType;
}

/**
 * Pre-response loading state for the chat stream — shown only in the gap
 * between the user sending a message and the first agent activity arriving.
 *
 * A single element: three dots carved out by a circular mask, with a
 * deeper-ink sheen flowing through them (`.typing-ink-dots` in index.css).
 * The dots never blink or move — the only motion is light passing through
 * still ink, which reads calmer than any blinking/bouncing loader.
 */
export function TypingIndicator({ executorType: _executorType = 'claude' }: TypingIndicatorProps) {
    return <div className="typing-ink-dots animate-fade-in" aria-hidden />;
}
