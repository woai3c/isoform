import * as DialogPrimitive from '@radix-ui/react-dialog';
import { X } from 'lucide-react';
import * as React from 'react';
import { cn } from '@/lib/utils';

const Dialog = DialogPrimitive.Root;
const DialogTrigger = DialogPrimitive.Trigger;
const DialogPortal = DialogPrimitive.Portal;
const DialogClose = DialogPrimitive.Close;

const DialogOverlay = React.forwardRef<
    React.ElementRef<typeof DialogPrimitive.Overlay>,
    React.ComponentPropsWithoutRef<typeof DialogPrimitive.Overlay>
>(({ className, ...props }, ref) => (
    <DialogPrimitive.Overlay
        ref={ref}
        className={cn(
            'fixed inset-0 z-50 bg-ink/40 backdrop-blur-[4px] animate-fade-in',
            className
        )}
        {...props}
    />
));
DialogOverlay.displayName = DialogPrimitive.Overlay.displayName;

const DialogContent = React.forwardRef<
    React.ElementRef<typeof DialogPrimitive.Content>,
    React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>
>(({ className, children, ...props }, ref) => (
    <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Content
            ref={ref}
            className={cn(
                'fixed top-1/2 left-1/2 z-50 w-[520px] max-h-[80vh] -translate-x-1/2 -translate-y-1/2 flex flex-col rounded-xl border border-border bg-panel shadow-[var(--shadow-card)] animate-rise-in',
                className
            )}
            {...props}
        >
            {children}
        </DialogPrimitive.Content>
    </DialogPortal>
));
DialogContent.displayName = DialogPrimitive.Content.displayName;

const DialogHeader = ({
    className,
    ...props
}: React.HTMLAttributes<HTMLDivElement>) => (
    <div
        className={cn(
            'flex items-center justify-between px-6 pt-5 pb-4 border-b border-border shrink-0',
            className
        )}
        {...props}
    />
);
DialogHeader.displayName = 'DialogHeader';

const DialogTitle = React.forwardRef<
    React.ElementRef<typeof DialogPrimitive.Title>,
    React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>
>(({ className, ...props }, ref) => (
    <DialogPrimitive.Title
        ref={ref}
        className={cn('text-lg font-semibold text-ink', className)}
        {...props}
    />
));
DialogTitle.displayName = DialogPrimitive.Title.displayName;

const DialogDescription = React.forwardRef<
    React.ElementRef<typeof DialogPrimitive.Description>,
    React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>
>(({ className, ...props }, ref) => (
    <DialogPrimitive.Description
        ref={ref}
        className={cn('text-sm text-muted', className)}
        {...props}
    />
));
DialogDescription.displayName = DialogPrimitive.Description.displayName;

const DialogCloseButton = React.forwardRef<
    React.ElementRef<typeof DialogPrimitive.Close>,
    React.ComponentPropsWithoutRef<typeof DialogPrimitive.Close>
>(({ className, ...props }, ref) => {
    return (
        <DialogPrimitive.Close
            ref={ref}
            className={cn(
                "flex items-center justify-center w-7 h-7 rounded-lg bg-transparent text-muted cursor-pointer transition-colors hover:bg-ink/[0.08] shrink-0 focus:outline-none focus-visible:ring-2 focus-visible:ring-border-focus disabled:pointer-events-none disabled:opacity-50",
                className,
            )}
            {...props}
        >
            <X className="h-4 w-4" />
            <span className="sr-only">Close</span>
        </DialogPrimitive.Close>
    );
});
DialogCloseButton.displayName = "DialogCloseButton";

export {
    Dialog,
    DialogPortal,
    DialogOverlay,
    DialogClose,
    DialogTrigger,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription,
    DialogCloseButton,
};
