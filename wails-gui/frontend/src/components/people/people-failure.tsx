// Who you know's failures and developer-only layer. A failure sits where the
// thing failed, in copper, with Try again in the accent; never red, never a
// toast.
import { createContext, useContext, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

/** Technical details, voice scores, raw reasons and the account-id field
 *  show only with developer tools on. */
export const PeopleDevToolsContext = createContext(false);

export function usePeopleDevTools(): boolean {
    return useContext(PeopleDevToolsContext);
}

export function PeopleFailureLine({
    text,
    onRetry,
    retryLabel,
    detail,
    className,
    children,
}: {
    text: string;
    /** Try again; resolve or throw, the parent clears the line when it worked. */
    onRetry?: () => unknown;
    retryLabel?: string;
    /** The raw reason, shown only with developer tools on. */
    detail?: string;
    className?: string;
    children?: ReactNode;
}) {
    const { t } = useTranslation('people');
    const devTools = usePeopleDevTools();
    const [busy, setBusy] = useState(false);
    const retry = onRetry
        ? async () => {
              if (busy) return;
              setBusy(true);
              try {
                  await onRetry();
              } catch {
                  // The parent keeps the line; pressing again tries again.
              } finally {
                  setBusy(false);
              }
          }
        : undefined;
    return (
        <p role="alert" data-people-failure="" className={cn('m-0 text-[12.5px] leading-relaxed text-copper', className)}>
            <span>{text}</span>
            {retry && (
                <button
                    type="button"
                    disabled={busy}
                    className="ms-1.5 cursor-pointer font-medium text-accent-2 underline-offset-2 hover:underline focus-visible:underline disabled:cursor-default disabled:opacity-60"
                    onClick={() => void retry()}
                >
                    {retryLabel ?? t('overview.retry')}
                </button>
            )}
            {children}
            {devTools && detail && <span className="mt-0.5 block font-mono text-[11px] text-muted">{detail}</span>}
        </p>
    );
}
