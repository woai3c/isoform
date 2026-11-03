import * as React from 'react';
import { cn } from '@/lib/utils';

const Textarea = React.forwardRef<
    HTMLTextAreaElement,
    React.TextareaHTMLAttributes<HTMLTextAreaElement>
>(({ className, ...props }, ref) => {
    return (
        <textarea
            className={cn(
                'flex min-h-[72px] w-full rounded-xl border border-border bg-panel px-3.5 py-3 text-sm text-ink transition-colors resize-y placeholder:text-muted focus:outline-2 focus:outline-accent-2/40 focus:border-accent-2/40 disabled:cursor-not-allowed disabled:opacity-50',
                className
            )}
            ref={ref}
            {...props}
        />
    );
});
Textarea.displayName = 'Textarea';

export { Textarea };
