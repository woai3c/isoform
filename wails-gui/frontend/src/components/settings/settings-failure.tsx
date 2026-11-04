import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

// Settings failures sit in place (settings-review v1.0.0 ST9–ST12): a change
// that fails puts the control back and shows one copper line under it with
// Try again, which repeats the change. A read that hasn't answered shows still
// rows with no words; after READ_DEADLINE_MS it shows the section's own
// failure line. No toasts, no red.

export interface SettingsFailure {
    text: string;
    retry?: () => void;
}

/** How long a read may stay unanswered before its failure line shows. */
export const READ_DEADLINE_MS = 10_000;

export function SettingsFailureLine({ failure, className = "" }: { failure: SettingsFailure | null; className?: string }) {
    const { t } = useTranslation("settings");
    if (!failure) return null;
    return (
        <p role="alert" data-settings-failure className={`text-[12px] text-copper ${className}`}>
            {failure.text}
            {failure.retry && (
                <>
                    {" "}
                    <button type="button" className="text-accent hover:underline" onClick={failure.retry}>
                        {t("settingRead.retry")}
                    </button>
                </>
            )}
        </p>
    );
}

/** One in-place failure line for a card or control. */
export function useSettingsFailure() {
    const [failure, setFailure] = useState<SettingsFailure | null>(null);
    const fail = useCallback(
        (text: string, retry?: () => void) =>
            setFailure({
                text,
                retry: retry
                    ? () => {
                          setFailure(null);
                          retry();
                      }
                    : undefined,
            }),
        [],
    );
    const clear = useCallback(() => setFailure(null), []);
    return { failure, fail, clear };
}

/**
 * Runs a change and reports a failure in place. `apply` shows the new value
 * before the call and `revert` puts the old one back when it fails; Try again
 * repeats the whole change.
 */
export function useSettingsChange(defaultText?: string) {
    const { t } = useTranslation("settings");
    const { failure, fail, clear } = useSettingsFailure();
    const text = defaultText ?? t("settingChange.failed");
    const run = useCallback(
        async function change(
            call: () => Promise<unknown>,
            options: { apply?: () => void; revert?: () => void; text?: string; onDone?: () => void } = {},
        ): Promise<boolean> {
            clear();
            options.apply?.();
            try {
                await call();
                options.onDone?.();
                return true;
            } catch {
                options.revert?.();
                fail(options.text ?? text, () => void change(call, options));
                return false;
            }
        },
        [clear, fail, text],
    );
    return { failure, fail, clear, run };
}

/** True once `pending` has lasted READ_DEADLINE_MS. */
export function useReadDeadline(pending: boolean, ms = READ_DEADLINE_MS): boolean {
    const [expired, setExpired] = useState(false);
    const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
    useEffect(() => {
        if (!pending) {
            setExpired(false);
            return;
        }
        timer.current = setTimeout(() => setExpired(true), ms);
        return () => {
            if (timer.current) clearTimeout(timer.current);
        };
    }, [pending, ms]);
    return expired;
}

/** Still rows where content will be, with no words, while a read is pending. */
export function SettingsPlaceholderRows({ rows = 2 }: { rows?: number }) {
    return (
        <div aria-hidden data-settings-placeholder className="flex flex-col gap-2 py-1">
            {Array.from({ length: rows }, (_, i) => (
                <div key={i} className="h-4 rounded bg-ink/[0.05]" style={{ width: i % 2 ? "60%" : "85%" }} />
            ))}
        </div>
    );
}
