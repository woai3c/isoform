import { useCallback, useEffect, useRef, useState } from 'react';
import type React from 'react';
import { Check, Copy } from 'lucide-react';
import { cn } from '@/lib/utils';

interface CopyButtonProps {
    text: string;
    label: string;
    copiedLabel: string;
    title?: string;
    className?: string;
    iconClassName?: string;
}

export function CopyButton({
    text,
    label,
    copiedLabel,
    title,
    className,
    iconClassName,
}: CopyButtonProps) {
    const [copied, setCopied] = useState(false);
    const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    useEffect(() => {
        return () => {
            if (timerRef.current) clearTimeout(timerRef.current);
        };
    }, []);

    const handleCopy = useCallback((e: React.MouseEvent<HTMLButtonElement>) => {
        e.stopPropagation();
        navigator.clipboard.writeText(text);
        setCopied(true);
        if (timerRef.current) clearTimeout(timerRef.current);
        timerRef.current = setTimeout(() => setCopied(false), 2000);
    }, [text]);

    return (
        <button
            type="button"
            className={cn(
                'inline-flex items-center gap-1 transition-colors cursor-pointer',
                className,
            )}
            onClick={handleCopy}
            title={title}
            aria-label={title ?? label}
        >
            {copied ? (
                <>
                    <Check className={cn('size-3', iconClassName)} />
                    {copiedLabel}
                </>
            ) : (
                <>
                    <Copy className={cn('size-3', iconClassName)} />
                    {label}
                </>
            )}
        </button>
    );
}
