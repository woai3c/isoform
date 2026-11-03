import type { ImgHTMLAttributes } from 'react';
import { ImageOff } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useSnapshotFallback } from '../hooks/useSnapshotFallback';
import { cn } from '../lib/utils';

type SnapshotImageProps = ImgHTMLAttributes<HTMLImageElement> & {
    /** Classes for the placeholder shown when the screenshot file is gone. */
    placeholderClassName?: string;
    /** Show the "No snapshot available" label next to the icon. */
    showLabel?: boolean;
};

/**
 * An activity screenshot that turns into a quiet placeholder when its file
 * cannot be loaded (never written, or removed by storage rotation) instead of
 * a broken image. See useSnapshotFallback for when it tries again.
 */
export function SnapshotImage({ src, placeholderClassName, showLabel = false, onError, ...imgProps }: SnapshotImageProps) {
    const { t } = useTranslation('activity');
    const fallback = useSnapshotFallback(src);

    if (fallback.missing || !src) {
        const label = t('session.noSnapshot');
        return (
            <div
                role="img"
                aria-label={label}
                className={cn('flex flex-col items-center justify-center gap-1.5', placeholderClassName)}
            >
                <ImageOff className="h-4 w-4" aria-hidden />
                {showLabel && <span className="text-xs">{label}</span>}
            </div>
        );
    }

    return (
        <img
            {...imgProps}
            src={src}
            onError={(e) => {
                fallback.onError();
                onError?.(e);
            }}
        />
    );
}
