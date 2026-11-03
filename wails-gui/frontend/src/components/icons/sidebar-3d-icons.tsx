import { useId } from 'react';

interface SidebarIconProps {
    className?: string;
}

/**
 * 3D open-folder icon for the "Custom Groups" parent section header.
 * Distinguishes from the closed GroupIcon3D used for individual groups.
 */
export function GroupParentIcon3D({ className }: SidebarIconProps) {
    const uid = useId().replace(/:/g, '');

    return (
        <svg viewBox="0 0 40 40" fill="none" className={className} xmlns="http://www.w3.org/2000/svg">
            <defs>
                {/* Tab - medium amber */}
                <linearGradient id={`opt${uid}`} x1="13" y1="7" x2="13" y2="13" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-icon-subtle)" />
                    <stop offset="1" stopColor="var(--color-icon-subtle)" />
                </linearGradient>
                {/* Back wall - dark */}
                <linearGradient id={`obk${uid}`} x1="20" y1="12" x2="20" y2="32" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-icon-subtle)" />
                    <stop offset="1" stopColor="var(--color-muted)" />
                </linearGradient>
                {/* Open lid left - brightest */}
                <linearGradient id={`oll${uid}`} x1="14" y1="16" x2="14" y2="28" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-panel)" />
                    <stop offset="1" stopColor="var(--color-icon-subtle)" />
                </linearGradient>
                {/* Open lid right */}
                <linearGradient id={`olr${uid}`} x1="28" y1="16" x2="28" y2="28" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-panel)" />
                    <stop offset="1" stopColor="var(--color-icon-subtle)" />
                </linearGradient>
                {/* Specular highlight */}
                <radialGradient id={`osp${uid}`} cx="16" cy="20" r="9" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-panel)" stopOpacity="0.55" />
                    <stop offset="1" stopColor="var(--color-panel)" stopOpacity="0" />
                </radialGradient>
            </defs>

            {/* Drop shadow */}
            <path d="M7.5 9L19 9L22 13L37.5 13L37.5 36.5L6.5 36.5Z" fill="var(--color-muted)" fillOpacity="0.15" />

            {/* Tab */}
            <path d="M6 8L18 8L21 12L6 12Z" fill={`url(#opt${uid})`} />
            {/* Back wall */}
            <path d="M5 12L36 12L36 35L5 35Z" fill={`url(#obk${uid})`} />

            {/* Open lid left (tilted forward - brightest) */}
            <path d="M3 15L20 15L20 30L5 35Z" fill={`url(#oll${uid})`} />
            {/* Open lid right */}
            <path d="M20 15L38 15L36 35L20 30Z" fill={`url(#olr${uid})`} />

            {/* Lid top edge highlight */}
            <line x1="3" y1="15" x2="38" y2="15" stroke="var(--color-panel)" strokeOpacity="0.55" strokeWidth="0.6" />
            {/* Center facet line */}
            <line x1="20" y1="15" x2="20" y2="30" stroke="var(--color-panel)" strokeOpacity="0.2" strokeWidth="0.4" />
            {/* Tab top edge highlight */}
            <line x1="6" y1="8" x2="18" y2="8" stroke="var(--color-panel)" strokeOpacity="0.55" strokeWidth="0.5" />
            {/* Tab right edge */}
            <line x1="18" y1="8" x2="21" y2="12" stroke="var(--color-panel)" strokeOpacity="0.25" strokeWidth="0.4" />

            {/* Specular on lid left */}
            <circle cx="14" cy="20" r="8" fill={`url(#osp${uid})`} />
        </svg>
    );
}

/**
 * 3D folder icon for custom sidebar groups.
 * Faceted folder shape with warm amber per-face gradients.
 */
export function GroupIcon3D({ className }: SidebarIconProps) {
    const uid = useId().replace(/:/g, '');

    return (
        <svg viewBox="0 0 40 40" fill="none" className={className} xmlns="http://www.w3.org/2000/svg">
            <defs>
                {/* Tab - medium amber */}
                <linearGradient id={`gt${uid}`} x1="13" y1="7" x2="13" y2="13" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-icon-subtle)" />
                    <stop offset="1" stopColor="var(--color-icon-subtle)" />
                </linearGradient>
                {/* Inner fold strip - darkest (depth) */}
                <linearGradient id={`gi${uid}`} x1="20" y1="12" x2="20" y2="16" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-icon-subtle)" />
                    <stop offset="1" stopColor="var(--color-muted)" />
                </linearGradient>
                {/* Front left - brightest (catches light) */}
                <linearGradient id={`fl${uid}`} x1="12" y1="16" x2="12" y2="35" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-panel)" />
                    <stop offset="1" stopColor="var(--color-icon-subtle)" />
                </linearGradient>
                {/* Front right - medium */}
                <linearGradient id={`fr${uid}`} x1="28" y1="16" x2="28" y2="35" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-icon-subtle)" />
                    <stop offset="1" stopColor="var(--color-icon-subtle)" />
                </linearGradient>
                {/* Specular highlight */}
                <radialGradient id={`sp${uid}`} cx="14" cy="22" r="9" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-panel)" stopOpacity="0.55" />
                    <stop offset="1" stopColor="var(--color-panel)" stopOpacity="0" />
                </radialGradient>
            </defs>

            {/* Drop shadow */}
            <path d="M7.5 9L19 9L22 13L37.5 13L37.5 36.5L6.5 36.5Z" fill="var(--color-muted)" fillOpacity="0.18" />

            {/* Tab */}
            <path d="M6 8L18 8L21 12L6 12Z" fill={`url(#gt${uid})`} />
            {/* Inner fold strip */}
            <path d="M5 12L36 12L36 16L5 16Z" fill={`url(#gi${uid})`} />
            {/* Front left (brightest) */}
            <path d="M5 16L20 16L20 35L5 35Z" fill={`url(#fl${uid})`} />
            {/* Front right */}
            <path d="M20 16L36 16L36 35L20 35Z" fill={`url(#fr${uid})`} />

            {/* Front top edge highlight */}
            <line x1="5" y1="16" x2="36" y2="16" stroke="var(--color-panel)" strokeOpacity="0.5" strokeWidth="0.6" />
            {/* Center facet line */}
            <line x1="20" y1="16" x2="20" y2="35" stroke="var(--color-panel)" strokeOpacity="0.2" strokeWidth="0.4" />
            {/* Tab top edge highlight */}
            <line x1="6" y1="8" x2="18" y2="8" stroke="var(--color-panel)" strokeOpacity="0.55" strokeWidth="0.5" />
            {/* Tab right edge */}
            <line x1="18" y1="8" x2="21" y2="12" stroke="var(--color-panel)" strokeOpacity="0.25" strokeWidth="0.4" />

            {/* Specular on front left */}
            <circle cx="14" cy="22" r="8" fill={`url(#sp${uid})`} />
        </svg>
    );
}

export function ChatIcon3D({ className }: SidebarIconProps) {
    const uid = useId().replace(/:/g, '');

    const bubblePath =
        'M36 27C36 29.8 33.8 32 31 32H16L6 38V9C6 6.2 8.2 4 11 4H31C33.8 4 36 6.2 36 9Z';

    return (
        <svg viewBox="0 0 40 40" fill="none" className={className} xmlns="http://www.w3.org/2000/svg">
            <defs>
                <linearGradient id={`bg${uid}`} x1="20" y1="4" x2="20" y2="38" gradientUnits="userSpaceOnUse">
                    <stop style={{ stopColor: 'var(--color-accent)' }} stopOpacity="0.78" />
                    <stop offset="1" style={{ stopColor: 'var(--color-accent)' }} />
                </linearGradient>
                <clipPath id={`cp${uid}`}>
                    <path d={bubblePath} />
                </clipPath>
                <radialGradient id={`hl${uid}`} cx="16" cy="10" r="12" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-panel)" stopOpacity="0.55" />
                    <stop offset="1" stopColor="var(--color-panel)" stopOpacity="0" />
                </radialGradient>
            </defs>

            {/* Drop shadow */}
            <path d={bubblePath} fill="var(--color-ink)" opacity="0.15" transform="translate(1, 1.5)" />

            {/* Main bubble */}
            <path d={bubblePath} fill={`url(#bg${uid})`} />

            {/* 3D effects (clipped to bubble) */}
            <g clipPath={`url(#cp${uid})`}>
                {/* Top highlight band */}
                <rect x="0" y="0" width="40" height="16" fill="var(--color-panel)" opacity="0.22" />
                {/* Bottom shadow band */}
                <rect x="0" y="24" width="40" height="20" fill="var(--color-ink)" opacity="0.12" />
                {/* Specular highlight */}
                <circle cx="16" cy="10" r="10" fill={`url(#hl${uid})`} />
            </g>

            {/* Top edge rim light */}
            <path d="M11 4H31C33.8 4 36 6.2 36 9" stroke="var(--color-panel)" strokeOpacity="0.35" strokeWidth="0.5" fill="none" />
        </svg>
    );
}

/**
 * 3D clock icon for the Automations sidebar section.
 * Circular clock face with per-half gradients and clock hands.
 * Warm purple/violet palette — distinct from terracotta chat and amber groups.
 */
export function AutomationsIcon3D({ className }: SidebarIconProps) {
    const uid = useId().replace(/:/g, '');

    return (
        <svg viewBox="0 0 40 40" fill="none" className={className} xmlns="http://www.w3.org/2000/svg">
            <defs>
                {/* Left half - lighter */}
                <linearGradient id={`al${uid}`} x1="10" y1="4" x2="10" y2="36" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-panel)" />
                    <stop offset="1" stopColor="var(--color-icon-subtle)" />
                </linearGradient>
                {/* Right half - darker */}
                <linearGradient id={`ar${uid}`} x1="30" y1="4" x2="30" y2="36" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-icon-subtle)" />
                    <stop offset="1" stopColor="var(--color-muted)" />
                </linearGradient>
                {/* Specular highlight */}
                <radialGradient id={`sp${uid}`} cx="15" cy="12" r="10" gradientUnits="userSpaceOnUse">
                    <stop stopColor="var(--color-panel)" stopOpacity="0.55" />
                    <stop offset="1" stopColor="var(--color-panel)" stopOpacity="0" />
                </radialGradient>
            </defs>

            {/* Drop shadow */}
            <circle cx="21" cy="21.5" r="16" fill="var(--color-muted)" fillOpacity="0.18" />

            {/* Left half */}
            <path d="M20 4A16 16 0 0 0 20 36Z" fill={`url(#al${uid})`} />
            {/* Right half */}
            <path d="M20 4A16 16 0 0 1 20 36Z" fill={`url(#ar${uid})`} />

            {/* Equator highlight */}
            <line x1="4" y1="20" x2="36" y2="20" stroke="var(--color-panel)" strokeOpacity="0.15" strokeWidth="0.4" />
            {/* Center facet line */}
            <line x1="20" y1="4" x2="20" y2="36" stroke="var(--color-panel)" strokeOpacity="0.2" strokeWidth="0.4" />

            {/* Top edge rim light */}
            <path d="M12 6.5A16 16 0 0 1 28 6.5" stroke="var(--color-panel)" strokeOpacity="0.4" strokeWidth="0.5" fill="none" />

            {/* Specular on upper left */}
            <circle cx="15" cy="12" r="9" fill={`url(#sp${uid})`} />

            {/* Clock hands */}
            {/* Minute hand (pointing to 12) */}
            <line x1="20" y1="20" x2="20" y2="10" stroke="var(--color-panel)" strokeOpacity="0.85" strokeWidth="1.5" strokeLinecap="round" />
            {/* Hour hand (pointing to 3) */}
            <line x1="20" y1="20" x2="27" y2="20" stroke="var(--color-panel)" strokeOpacity="0.7" strokeWidth="1.5" strokeLinecap="round" />
            {/* Center dot */}
            <circle cx="20" cy="20" r="1.5" fill="var(--color-panel)" fillOpacity="0.9" />
        </svg>
    );
}
