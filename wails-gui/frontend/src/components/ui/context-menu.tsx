import * as React from 'react';
import * as ContextMenuPrimitive from '@radix-ui/react-context-menu';
import { cn } from '@/lib/utils';

const ContextMenu = ContextMenuPrimitive.Root;
const ContextMenuTrigger = ContextMenuPrimitive.Trigger;

const ContextMenuContent = React.forwardRef<
    React.ComponentRef<typeof ContextMenuPrimitive.Content>,
    React.ComponentPropsWithoutRef<typeof ContextMenuPrimitive.Content>
>(({ className, ...props }, ref) => (
    <ContextMenuPrimitive.Portal>
        <ContextMenuPrimitive.Content
            ref={ref}
            className={cn(
                'z-50 min-w-[160px] rounded-lg border border-border bg-panel p-1 text-ink shadow-[var(--shadow-card)]',
                'animate-fade-in',
                className
            )}
            {...props}
        />
    </ContextMenuPrimitive.Portal>
));
ContextMenuContent.displayName = 'ContextMenuContent';

const ContextMenuItem = React.forwardRef<
    React.ComponentRef<typeof ContextMenuPrimitive.Item>,
    React.ComponentPropsWithoutRef<typeof ContextMenuPrimitive.Item> & {
        destructive?: boolean;
    }
>(({ className, destructive, ...props }, ref) => (
    <ContextMenuPrimitive.Item
        ref={ref}
        className={cn(
            'relative flex items-center gap-2 rounded-md px-2 py-1.5 text-[13px] cursor-default select-none outline-none transition-colors',
            'focus:bg-ink/[0.06] data-[highlighted]:bg-ink/[0.06]',
            'data-[disabled]:opacity-40 data-[disabled]:pointer-events-none',
            destructive && 'text-error focus:bg-error-bg',
            className
        )}
        {...props}
    />
));
ContextMenuItem.displayName = 'ContextMenuItem';

const ContextMenuSeparator = React.forwardRef<
    React.ComponentRef<typeof ContextMenuPrimitive.Separator>,
    React.ComponentPropsWithoutRef<typeof ContextMenuPrimitive.Separator>
>(({ className, ...props }, ref) => (
    <ContextMenuPrimitive.Separator
        ref={ref}
        className={cn('my-1 h-px bg-border', className)}
        {...props}
    />
));
ContextMenuSeparator.displayName = 'ContextMenuSeparator';

export {
    ContextMenu,
    ContextMenuTrigger,
    ContextMenuContent,
    ContextMenuItem,
    ContextMenuSeparator,
};
