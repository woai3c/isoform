import * as React from 'react';
import { Command as CommandPrimitive } from 'cmdk';
import * as DialogPrimitive from '@radix-ui/react-dialog';
import { SearchIcon } from '@/assets/icons/SearchIcon';
import { cn } from '@/lib/utils';

const Command = React.forwardRef<
    React.ElementRef<typeof CommandPrimitive>,
    React.ComponentPropsWithoutRef<typeof CommandPrimitive>
>(({ className, ...props }, ref) => (
    <CommandPrimitive
        ref={ref}
        className={cn(
            'flex h-full w-full flex-col overflow-hidden rounded-xl bg-panel text-ink',
            className,
        )}
        {...props}
    />
));
Command.displayName = CommandPrimitive.displayName;

function CommandDialog({
    children,
    title,
    description,
    contentClassName,
    onEscapeKeyDown,
    commandProps,
    ...props
}: DialogPrimitive.DialogProps & {
    /** Read by screen readers only; the dialog has no visible heading. */
    title: string;
    description: string;
    /** Extra classes for the panel (a wider palette, a taller list). */
    contentClassName?: string;
    /** Asked before Escape closes the dialog; preventDefault keeps it open. */
    onEscapeKeyDown?: DialogPrimitive.DialogContentProps['onEscapeKeyDown'];
    /** Passed to cmdk: a controlled selection, shouldFilter, loop. */
    commandProps?: Omit<React.ComponentPropsWithoutRef<typeof CommandPrimitive>, 'children' | 'className'>;
}) {
    return (
        <DialogPrimitive.Root {...props}>
            <DialogPrimitive.Portal>
                <DialogPrimitive.Overlay className="launchpad-overlay fixed inset-0 z-[60] bg-ink/40 backdrop-blur-[4px]" />
                <DialogPrimitive.Content
                    onEscapeKeyDown={onEscapeKeyDown}
                    className={cn(
                        'launchpad-panel fixed top-[20%] left-1/2 z-[60] w-[520px] max-h-[60vh] -translate-x-1/2 flex flex-col rounded-xl border border-border bg-panel shadow-[var(--shadow-card)] overflow-hidden origin-top',
                        contentClassName,
                    )}
                >
                    <DialogPrimitive.Title className="sr-only">{title}</DialogPrimitive.Title>
                    <DialogPrimitive.Description className="sr-only">{description}</DialogPrimitive.Description>
                    <Command
                        {...commandProps}
                        className="min-h-0 flex-1 overflow-visible [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-muted [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-wider"
                    >
                        {children}
                    </Command>
                </DialogPrimitive.Content>
            </DialogPrimitive.Portal>
        </DialogPrimitive.Root>
    );
}

const CommandInput = React.forwardRef<
    React.ElementRef<typeof CommandPrimitive.Input>,
    React.ComponentPropsWithoutRef<typeof CommandPrimitive.Input> & {
        /** Shown at the right end of the input row (a result count). */
        trailing?: React.ReactNode;
    }
>(({ className, trailing, ...props }, ref) => (
    <div className="flex items-center gap-2 border-b border-border px-3" cmdk-input-wrapper="">
        <SearchIcon className="h-4 w-4 shrink-0 text-muted" />
        <CommandPrimitive.Input
            ref={ref}
            className={cn(
                'flex h-11 w-full rounded-md bg-transparent py-3 text-sm text-ink outline-none placeholder:text-muted disabled:cursor-not-allowed disabled:opacity-50',
                className,
            )}
            {...props}
        />
        {trailing}
    </div>
));
CommandInput.displayName = CommandPrimitive.Input.displayName;

const CommandList = React.forwardRef<
    React.ElementRef<typeof CommandPrimitive.List>,
    React.ComponentPropsWithoutRef<typeof CommandPrimitive.List>
>(({ className, ...props }, ref) => (
    <CommandPrimitive.List
        ref={ref}
        className={cn('max-h-[300px] overflow-y-auto overflow-x-hidden p-1', className)}
        {...props}
    />
));
CommandList.displayName = CommandPrimitive.List.displayName;

const CommandEmpty = React.forwardRef<
    React.ElementRef<typeof CommandPrimitive.Empty>,
    React.ComponentPropsWithoutRef<typeof CommandPrimitive.Empty>
>((props, ref) => (
    <CommandPrimitive.Empty ref={ref} className="py-6 text-center text-sm text-muted" {...props} />
));
CommandEmpty.displayName = CommandPrimitive.Empty.displayName;

const CommandGroup = React.forwardRef<
    React.ElementRef<typeof CommandPrimitive.Group>,
    React.ComponentPropsWithoutRef<typeof CommandPrimitive.Group>
>(({ className, ...props }, ref) => (
    <CommandPrimitive.Group
        ref={ref}
        className={cn(
            'p-1 [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5',
            className,
        )}
        {...props}
    />
));
CommandGroup.displayName = CommandPrimitive.Group.displayName;

const CommandSeparator = React.forwardRef<
    React.ElementRef<typeof CommandPrimitive.Separator>,
    React.ComponentPropsWithoutRef<typeof CommandPrimitive.Separator>
>(({ className, ...props }, ref) => (
    <CommandPrimitive.Separator
        ref={ref}
        className={cn('-mx-1 h-px bg-border', className)}
        {...props}
    />
));
CommandSeparator.displayName = CommandPrimitive.Separator.displayName;

const CommandItem = React.forwardRef<
    React.ElementRef<typeof CommandPrimitive.Item>,
    React.ComponentPropsWithoutRef<typeof CommandPrimitive.Item>
>(({ className, ...props }, ref) => (
    <CommandPrimitive.Item
        ref={ref}
        className={cn(
            'relative flex items-center gap-2 rounded-lg px-2 py-2 text-sm text-ink cursor-pointer select-none outline-none data-[selected=true]:bg-surface-active data-[disabled=true]:pointer-events-none data-[disabled=true]:opacity-50 transition-[background-color,color] duration-[120ms] ease-[cubic-bezier(0.16,1,0.3,1)]',
            className,
        )}
        {...props}
    />
));
CommandItem.displayName = CommandPrimitive.Item.displayName;

export {
    Command,
    CommandDialog,
    CommandInput,
    CommandList,
    CommandEmpty,
    CommandGroup,
    CommandSeparator,
    CommandItem,
};
