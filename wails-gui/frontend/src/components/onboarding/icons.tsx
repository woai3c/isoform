/**
 * Onboarding Icon Design System
 *
 * Design language for onboarding-specific icons. These replace generic lucide-react
 * line icons with warm, dimensional SVGs that match the brand personality.
 *
 * Rules:
 *   1. viewBox="0 0 24 24", designed to render cleanly at 16-20px
 *   2. Use currentColor for all fills — icons inherit color from parent
 *   3. Depth via opacity layers (0.12, 0.18, 0.25, 0.5) on filled shapes
 *   4. Rounded, organic forms — no sharp corners or geometric rigidity
 *   5. Filled shapes only, no strokes
 *   6. No gradients — flat fills with opacity create warmth without complexity
 *   7. Keep path count low (3-5 shapes) for clean rendering at small sizes
 */

interface OnboardingIconProps {
    className?: string;
}

/**
 * Claude Code assistant — a soft four-point spark over a rounded tile,
 * layered for depth. An abstract mark, not a vendor logo.
 */
export function ClaudeCodeAssistantIcon({ className }: OnboardingIconProps) {
    return (
        <svg viewBox="0 0 24 24" fill="none" className={className} xmlns="http://www.w3.org/2000/svg">
            {/* Tile shadow */}
            <rect x="3" y="3.5" width="18" height="18" rx="5" fill="currentColor" fillOpacity="0.12" />
            {/* Tile */}
            <rect x="2.5" y="2.5" width="18" height="18" rx="5" fill="currentColor" fillOpacity="0.2" />
            {/* Spark glow */}
            <circle cx="11.5" cy="11.5" r="5.5" fill="currentColor" fillOpacity="0.18" />
            {/* Spark */}
            <path
                d="M11.5 5.5C11.9 8.6 12.9 9.6 16 10c-3.1 .4-4.1 1.4-4.5 4.5C11.1 11.4 10.1 10.4 7 10c3.1-.4 4.1-1.4 4.5-4.5Z"
                fill="currentColor"
                fillOpacity="0.6"
            />
            {/* Small companion spark */}
            <circle cx="16.5" cy="15.5" r="1.4" fill="currentColor" fillOpacity="0.45" />
        </svg>
    );
}

/**
 * Codex assistant — a terminal prompt chevron inside a rounded tile with a
 * cursor block, layered for depth. An abstract mark, not a vendor logo.
 */
export function CodexAssistantIcon({ className }: OnboardingIconProps) {
    return (
        <svg viewBox="0 0 24 24" fill="none" className={className} xmlns="http://www.w3.org/2000/svg">
            {/* Tile shadow */}
            <rect x="3" y="3.5" width="18" height="18" rx="5" fill="currentColor" fillOpacity="0.12" />
            {/* Tile */}
            <rect x="2.5" y="2.5" width="18" height="18" rx="5" fill="currentColor" fillOpacity="0.2" />
            {/* Prompt chevron, built from two rounded bars */}
            <rect x="6.5" y="7.5" width="6" height="2.4" rx="1.2" fill="currentColor" fillOpacity="0.55" transform="rotate(40 9.5 8.7)" />
            <rect x="6.5" y="12.3" width="6" height="2.4" rx="1.2" fill="currentColor" fillOpacity="0.55" transform="rotate(-40 9.5 13.5)" />
            {/* Cursor block */}
            <rect x="12.5" y="13.2" width="4.5" height="2.4" rx="1.2" fill="currentColor" fillOpacity="0.4" />
        </svg>
    );
}
