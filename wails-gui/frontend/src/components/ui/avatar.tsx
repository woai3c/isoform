import * as React from 'react';
import { cn } from '@/lib/utils';

interface AvatarProps extends React.HTMLAttributes<HTMLDivElement> {
    src?: string | null;
    alt?: string;
    fallback?: string;
    size?: 'xs' | 'sm' | 'md' | 'lg';
}

const sizeClasses = {
    xs: 'h-5 w-5 text-[9px]',
    sm: 'h-6 w-6 text-[10px]',
    md: 'h-7 w-7 text-[11px]',
    lg: 'h-8 w-8 text-xs',
};

export function Avatar({
    src,
    alt = 'User avatar',
    fallback,
    size = 'md',
    className,
    ...props
}: AvatarProps) {
    const [imgError, setImgError] = React.useState(false);

    const initials = React.useMemo(() => {
        if (fallback) {
            return fallback
                .split(' ')
                .map((word) => word[0])
                .slice(0, 2)
                .join('')
                .toUpperCase();
        }
        return '?';
    }, [fallback]);

    const showFallback = !src || imgError;

    return (
        <div
            className={cn(
                // Neutral warm fill + ink initials. Avatars are chrome, not
                // status — they stay monochrome like every other surface.
                'relative flex shrink-0 items-center justify-center overflow-hidden rounded-full bg-surface-active text-ink font-medium',
                sizeClasses[size],
                className
            )}
            {...props}
        >
            {showFallback ? (
                <span>{initials}</span>
            ) : (
                <img
                    src={src}
                    alt={alt}
                    className="h-full w-full object-cover"
                    onError={() => setImgError(true)}
                />
            )}
        </div>
    );
}
