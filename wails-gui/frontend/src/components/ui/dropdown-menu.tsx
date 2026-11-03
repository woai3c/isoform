import * as React from 'react';
import * as DropdownMenuPrimitive from '@radix-ui/react-dropdown-menu';
import { cn } from '@/lib/utils';

const DropdownMenu = DropdownMenuPrimitive.Root;
const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger;
const DropdownMenuSub = DropdownMenuPrimitive.Sub;

const DropdownMenuContent = React.forwardRef<
    React.ComponentRef<typeof DropdownMenuPrimitive.Content>,
    React.ComponentPropsWithoutRef<typeof DropdownMenuPrimitive.Content>
>(({ className, sideOffset = 4, ...props }, ref) => (
    <DropdownMenuPrimitive.Portal>
        <DropdownMenuPrimitive.Content
            ref={ref}
            sideOffset={sideOffset}
            className={cn(
                // Littlebird menu chrome: xl radius, strong hairline border,
                // whisper shadow — the border carries the definition, not a
                // floaty drop shadow.
                'z-50 min-w-[12rem] rounded-xl border border-border-strong bg-panel p-1 text-ink shadow-[var(--shadow-soft)]',
                'data-[side=bottom]:translate-y-1 data-[side=top]:-translate-y-1',
                'animate-fade-in',
                className
            )}
            {...props}
        />
    </DropdownMenuPrimitive.Portal>
));
DropdownMenuContent.displayName = 'DropdownMenuContent';

const DropdownMenuItem = React.forwardRef<
    React.ComponentRef<typeof DropdownMenuPrimitive.Item>,
    React.ComponentPropsWithoutRef<typeof DropdownMenuPrimitive.Item> & {
        destructive?: boolean;
    }
>(({ className, destructive, ...props }, ref) => (
    <DropdownMenuPrimitive.Item
        ref={ref}
        className={cn(
            'relative flex items-center gap-3 rounded-md px-3 py-2 text-[12.5px] cursor-default select-none outline-none transition-colors',
            '[&>svg]:size-3.5 [&>svg]:shrink-0',
            'focus:bg-surface-active data-[highlighted]:bg-surface-active',
            'data-[disabled]:opacity-40 data-[disabled]:pointer-events-none',
            destructive && 'text-error focus:bg-error-bg data-[highlighted]:bg-error-bg',
            className
        )}
        {...props}
    />
));
DropdownMenuItem.displayName = 'DropdownMenuItem';

const DropdownMenuSeparator = React.forwardRef<
    React.ComponentRef<typeof DropdownMenuPrimitive.Separator>,
    React.ComponentPropsWithoutRef<typeof DropdownMenuPrimitive.Separator>
>(({ className, ...props }, ref) => (
    <DropdownMenuPrimitive.Separator
        ref={ref}
        className={cn('mx-3 my-1 h-px bg-border', className)}
        {...props}
    />
));
DropdownMenuSeparator.displayName = 'DropdownMenuSeparator';

const DropdownMenuLabel = React.forwardRef<
    React.ComponentRef<typeof DropdownMenuPrimitive.Label>,
    React.ComponentPropsWithoutRef<typeof DropdownMenuPrimitive.Label>
>(({ className, ...props }, ref) => (
    <DropdownMenuPrimitive.Label
        ref={ref}
        className={cn('px-2 py-1.5 text-xs font-medium text-muted', className)}
        {...props}
    />
));
DropdownMenuLabel.displayName = 'DropdownMenuLabel';

const DropdownMenuSubTrigger = React.forwardRef<
    React.ComponentRef<typeof DropdownMenuPrimitive.SubTrigger>,
    React.ComponentPropsWithoutRef<typeof DropdownMenuPrimitive.SubTrigger>
>(({ className, children, ...props }, ref) => (
    <DropdownMenuPrimitive.SubTrigger
        ref={ref}
        className={cn(
            'relative flex items-center gap-3 rounded-md px-3 py-2 text-[12.5px] cursor-default select-none outline-none transition-colors',
            '[&>svg]:size-3.5 [&>svg]:shrink-0',
            'focus:bg-surface-active data-[highlighted]:bg-surface-active',
            'data-[state=open]:bg-surface-active',
            className
        )}
        {...props}
    >
        {children}
        <svg
            className="ml-auto h-3.5 w-3.5 text-muted"
            xmlns="http://www.w3.org/2000/svg"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
        >
            <path d="m9 18 6-6-6-6" />
        </svg>
    </DropdownMenuPrimitive.SubTrigger>
));
DropdownMenuSubTrigger.displayName = 'DropdownMenuSubTrigger';

const DropdownMenuSubContent = React.forwardRef<
    React.ComponentRef<typeof DropdownMenuPrimitive.SubContent>,
    React.ComponentPropsWithoutRef<typeof DropdownMenuPrimitive.SubContent>
>(({ className, ...props }, ref) => (
    <DropdownMenuPrimitive.Portal>
        <DropdownMenuPrimitive.SubContent
            ref={ref}
            className={cn(
                'z-50 min-w-[12rem] rounded-xl border border-border-strong bg-panel p-1 text-ink shadow-[var(--shadow-soft)]',
                'animate-fade-in',
                className
            )}
            {...props}
        />
    </DropdownMenuPrimitive.Portal>
));
DropdownMenuSubContent.displayName = 'DropdownMenuSubContent';

export {
    DropdownMenu,
    DropdownMenuTrigger,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuLabel,
    DropdownMenuSub,
    DropdownMenuSubTrigger,
    DropdownMenuSubContent,
};
