import * as DialogPrimitive from '@radix-ui/react-dialog';
import { X } from 'lucide-react';
import * as React from 'react';
import { cn } from '@/lib/utils';

const Sheet = DialogPrimitive.Root;
const SheetTrigger = DialogPrimitive.Trigger;
const SheetPortal = DialogPrimitive.Portal;
const SheetClose = DialogPrimitive.Close;

const SheetOverlay = React.forwardRef<
    React.ElementRef<typeof DialogPrimitive.Overlay>,
    React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
    <DialogPrimitive.Overlay
        ref={ref}
        className={cn(
            'fixed inset-0 z-50 bg-ink/30 backdrop-blur-[2px] animate-fade-in',
            className
        )}
        {...props}
    />
));
SheetOverlay.displayName = 'SheetOverlay';

const SheetContent = React.forwardRef<
    React.ElementRef<typeof DialogPrimitive.Content>,
    React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>
>(({ className, children, ...props }, ref) => (
    <SheetPortal>
        <SheetOverlay />
        <DialogPrimitive.Content
            ref={ref}
            className={cn(
                'fixed right-0 top-0 bottom-0 z-50 w-[min(520px,90vw)] flex flex-col bg-panel border-l border-border shadow-[var(--shadow-card)]',
                'transition-transform duration-200 ease-out will-change-transform',
                'data-[state=open]:translate-x-0 data-[state=closed]:translate-x-full',
                className
            )}
            {...props}
        >
            {children}
        </DialogPrimitive.Content>
    </SheetPortal>
));
SheetContent.displayName = 'SheetContent';

const SheetHeader = ({
    className,
    ...props
}: React.HTMLAttributes<HTMLDivElement>) => (
    <div
        className={cn('flex items-start justify-between gap-3 px-6 pt-6 pb-4 shrink-0', className)}
        {...props}
    />
);
SheetHeader.displayName = 'SheetHeader';

const SheetTitle = React.forwardRef<
    React.ElementRef<typeof DialogPrimitive.Title>,
    React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(({ className, ...props }, ref) => (
    <DialogPrimitive.Title
        ref={ref}
        className={cn('text-[18px] font-semibold text-ink leading-tight tracking-tight', className)}
        {...props}
    />
));
SheetTitle.displayName = 'SheetTitle';

const SheetDescription = React.forwardRef<
    React.ElementRef<typeof DialogPrimitive.Description>,
    React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(({ className, ...props }, ref) => (
    <DialogPrimitive.Description
        ref={ref}
        className={cn('text-[12px] text-muted', className)}
        {...props}
    />
));
SheetDescription.displayName = 'SheetDescription';

function SheetCloseButton() {
    return (
        <DialogPrimitive.Close className="flex items-center justify-center w-7 h-7 rounded-lg bg-transparent text-muted cursor-pointer transition-colors hover:bg-ink/[0.08] shrink-0">
            <X className="h-4 w-4" />
            <span className="sr-only">Close</span>
        </DialogPrimitive.Close>
    );
}

export {
    Sheet,
    SheetPortal,
    SheetOverlay,
    SheetClose,
    SheetTrigger,
    SheetContent,
    SheetHeader,
    SheetTitle,
    SheetDescription,
    SheetCloseButton,
};
