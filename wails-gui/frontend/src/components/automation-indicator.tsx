import { useId } from 'react';

const AUTOMATION_BASE = 'var(--color-automation)';
const AUTOMATION_DEEP = 'var(--color-automation-deep)';
const AUTOMATION_LIGHT = 'var(--color-automation-light)';

interface AutomationIndicatorProps {
    disabled?: boolean;
    running?: boolean;
    size?: number;
}

/**
 * Small faceted-clock indicator for automation items.
 * Inline SVG with per-facet gradients to give a 3D appearance at icon size.
 *
 * When `running` is true the second hand rotates and a themed glow pulses
 * around the icon via an orbit-ring pattern.
 */
export function AutomationIndicator({ disabled, running, size = 10 }: AutomationIndicatorProps) {
    const uid = useId().replace(/:/g, '');
    const sizeStyle = { width: size, height: size };

    if (disabled) {
        return (
            <svg style={sizeStyle} viewBox="0 0 12 12" fill="none">
                <circle cx="6" cy="6" r="5" fill="none" stroke="currentColor" strokeWidth="0.9" opacity="0.35" />
                <line x1="6" y1="6" x2="6" y2="3" stroke="currentColor" strokeWidth="0.9" strokeLinecap="round" opacity="0.3" />
                <line x1="6" y1="6" x2="8.5" y2="6" stroke="currentColor" strokeWidth="0.9" strokeLinecap="round" opacity="0.25" />
            </svg>
        );
    }

    const gem = (
        <svg
            className={running ? 'automation-clock' : undefined}
            style={{
                ...sizeStyle,
                display: 'block',
                position: 'relative',
                zIndex: 1,
                ...(running ? {
                    filter: 'drop-shadow(0 0 2.5px color-mix(in srgb, var(--color-automation) 50%, transparent)) drop-shadow(0 0 5px color-mix(in srgb, var(--color-automation-light) 20%, transparent))',
                    animation: 'automation-glow 2s ease-in-out infinite',
                } : {}),
            }}
            viewBox="0 0 12 12"
            fill="none"
        >
            <defs>
                <linearGradient id={`al${uid}`} x1="3" y1="1" x2="3" y2="11" gradientUnits="userSpaceOnUse">
                    <stop stopColor={AUTOMATION_LIGHT} />
                    <stop offset="1" stopColor={AUTOMATION_BASE} />
                </linearGradient>
                <linearGradient id={`ar${uid}`} x1="9" y1="1" x2="9" y2="11" gradientUnits="userSpaceOnUse">
                    <stop stopColor={AUTOMATION_BASE} />
                    <stop offset="1" stopColor={AUTOMATION_DEEP} />
                </linearGradient>
                <radialGradient id={`sp${uid}`} cx="4.5" cy="4" r="4" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-solid-btn-fg)" stopOpacity="0.5" />
                    <stop offset="1" stopColor="var(--color-solid-btn-fg)" stopOpacity="0" />
                </radialGradient>
            </defs>
            {/* Left half - lighter */}
            <path d="M6 1A5 5 0 0 0 6 11Z" fill={`url(#al${uid})`} />
            {/* Right half - darker */}
            <path d="M6 1A5 5 0 0 1 6 11Z" fill={`url(#ar${uid})`} />
            {/* Specular highlight */}
            <circle cx="4.5" cy="4" r="3.5" fill={`url(#sp${uid})`} />
            {/* Top rim light */}
            <path d="M4 1.5A5 5 0 0 1 8 1.5" stroke="var(--color-solid-btn-fg)" strokeOpacity="0.4" strokeWidth="0.4" fill="none" />
            {/* Clock hands - minute (static) */}
            <line x1="6" y1="6" x2="6" y2="3.2" stroke="var(--color-solid-btn-fg)" strokeOpacity="0.85" strokeWidth="0.9" strokeLinecap="round" />
            {/* Clock hands - hour (static) */}
            <line x1="6" y1="6" x2="8.2" y2="6" stroke="var(--color-solid-btn-fg)" strokeOpacity="0.7" strokeWidth="0.9" strokeLinecap="round" />
            {/* Second hand - rotates when running */}
            {running && (
                <line
                    x1="6" y1="6" x2="6" y2="2.5"
                    stroke="var(--color-solid-btn-fg)" strokeOpacity="0.6" strokeWidth="0.5" strokeLinecap="round"
                    style={{
                        transformOrigin: '6px 6px',
                        animation: 'automation-second-hand 3s linear infinite',
                    }}
                />
            )}
            {/* Center dot */}
            <circle cx="6" cy="6" r="0.5" fill="var(--color-solid-btn-fg)" fillOpacity="0.9" />
        </svg>
    );

    if (!running) return gem;

    return (
        <span className="automation-wrap">
            <span className="automation-orbit-ring" style={{ color: AUTOMATION_LIGHT }} />
            {gem}
        </span>
    );
}
