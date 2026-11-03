import { useId } from 'react';
import { cn } from '@/lib/utils';

// Programmatic squircle app icon for automation cards: violet
// identifies Isoform's work, with a subtle top-light overlay and the app's
// first letter. The former crystal-era jelly entrance/hover animations and the
// offset blob shadow were removed (#1908 follow-up) — the icon is static.

interface AutomationAppIconProps {
    appName: string;
    /** Base64-encoded SVG icon from disk cache. Falls back to first letter if not provided. */
    aiIcon?: string;
    size?: number;
    dimmed?: boolean;
}

// Squircle — 12 cubic-bezier segments with continuous curvature.
const SQUIRCLE_PATH =
    'M50 0C76 0 84 0 89 2' +
    'C93 4 96 7 98 11' +
    'C100 16 100 24 100 50' +
    'C100 76 100 84 98 89' +
    'C96 93 93 96 89 98' +
    'C84 100 76 100 50 100' +
    'C24 100 16 100 11 98' +
    'C7 96 4 93 2 89' +
    'C0 84 0 76 0 50' +
    'C0 24 0 16 2 11' +
    'C4 7 7 4 11 2' +
    'C16 0 24 0 50 0Z';

function getIconInitial(appName: string) {
    const trimmed = appName.trim();
    if (!trimmed) return '?';
    return trimmed[0].toUpperCase();
}

export function AutomationAppIcon({
    appName,
    aiIcon,
    size = 40,
    dimmed,
}: AutomationAppIconProps) {
    const uid = useId().replace(/:/g, '');
    const fillColor = 'var(--color-solid-btn-bg)';
    const iconSrc = aiIcon ? `data:image/svg+xml;base64,${aiIcon}` : '';
    const initial = getIconInitial(appName);
    const initialSize = Math.max(14, Math.round(size * 0.4));

    return (
        <div
            className={cn('relative shrink-0', dimmed && 'grayscale-[0.85] opacity-50')}
            style={{ width: size, height: size }}
        >
            {iconSrc ? (
                <img
                    src={iconSrc}
                    alt={appName ? `${appName} icon` : 'Automation icon'}
                    className="h-full w-full object-cover"
                    style={{ borderRadius: '24%' }}
                />
            ) : (
                <>
                    <svg className="absolute h-full w-full" viewBox="0 0 100 100" aria-hidden="true">
                        <defs>
                            <linearGradient id={`hl-${uid}`} x1="0" y1="0" x2="0" y2="1">
                                <stop offset="0%" stopColor="var(--color-panel)" stopOpacity="0.15" />
                                <stop offset="50%" stopColor="var(--color-panel)" stopOpacity="0.03" />
                                <stop offset="100%" stopColor="var(--color-ink)" stopOpacity="0.06" />
                            </linearGradient>
                        </defs>
                        <path d={SQUIRCLE_PATH} fill={fillColor} />
                        <path d={SQUIRCLE_PATH} fill={`url(#hl-${uid})`} />
                    </svg>
                    <div
                        className="absolute inset-0 flex items-center justify-center select-none"
                        style={{ fontSize: initialSize }}
                    >
                        <span
                            className="font-semibold text-solid-btn-fg/90"
                            style={{ textShadow: 'var(--shadow-soft)' }}
                        >
                            {initial}
                        </span>
                    </div>
                </>
            )}
        </div>
    );
}
