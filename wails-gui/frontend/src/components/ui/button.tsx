import { Slot } from '@radix-ui/react-slot';
import { cva, type VariantProps } from 'class-variance-authority';
import * as React from 'react';
import { cn } from '@/lib/utils';

const buttonVariants = cva(
    'inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-semibold leading-none transition-all cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-2/40 focus-visible:ring-offset-2 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg]:translate-y-px',
    {
        variants: {
            variant: {
                // Primary action (Continue / Get started / Finish).
                // Violet fill with the paired foreground in both themes.
                default:
                    'patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] hover:opacity-90',
                // Secondary / brand / interactive-atmosphere.
                // Accent sample-text on background, 1px sample-border.
                secondary:
                    'border border-[var(--color-sample-border)] bg-background text-[var(--color-sample-text)] hover:bg-[var(--color-sample-text)]/[0.06]',
                // Integrations "Connect" state: filled neutral surface.
                // Light: btn-connect-bg + sample-border. Dark:
                // translucent neutral glass skin with backdrop blur for a
                // glass look. Accent foreground in both modes.
                connect:
                    'border border-[var(--color-sample-border)] bg-[var(--color-btn-connect-bg)] text-[var(--color-sample-text)] hover:opacity-90 dark:border-sample-border dark:bg-panel-warm dark:backdrop-blur-[4px] dark:transition-[background-color,border-color,transform] dark:hover:opacity-100 dark:hover:bg-panel-warm dark:hover:border-sample-border dark:active:translate-y-px',
                ghost: 'rounded-full border border-border bg-transparent text-ink hover:bg-ink/[0.04]',
                outline:
                    'border border-border bg-transparent text-muted hover:bg-ink/[0.04] hover:border-border-strong',
                destructive:
                    'bg-transparent text-error hover:bg-error-bg',
                link: 'text-accent underline-offset-4 hover:underline',
                // The quietest action beside other Buttons (RC 1): muted
                // words, a warm wash on hover, no border.
                quiet: 'bg-transparent text-muted hover:bg-surface-hover hover:text-ink',
            },
            size: {
                default: 'h-10 px-[18px] py-2.5 rounded-full',
                sm: 'h-8 px-3 text-xs rounded-lg',
                lg: 'h-11 px-8 rounded-full',
                icon: 'h-8 w-8 rounded-lg',
            },
        },
        defaultVariants: {
            variant: 'default',
            size: 'default',
        },
    }
);

export interface ButtonProps
    extends React.ButtonHTMLAttributes<HTMLButtonElement>,
        VariantProps<typeof buttonVariants> {
    asChild?: boolean;
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
    ({ className, variant, size, asChild = false, ...props }, ref) => {
        const Comp = asChild ? Slot : 'button';
        return (
            <Comp
                className={cn(buttonVariants({ variant, size, className }))}
                ref={ref}
                {...props}
            />
        );
    }
);
Button.displayName = 'Button';

export { Button, buttonVariants };
