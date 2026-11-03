import { memo } from "react";
import { Loader2, AlertTriangle, X } from "lucide-react";

interface CompactionBannerProps {
    state: "compacting" | "error" | "idle" | undefined;
    error?: string;
    onDismiss?: () => void;
}

/**
 * CompactionBanner sits between the message list and the composer to
 * surface the auto-compaction lifecycle. Two visible states only:
 *
 *   - compacting: background summarisation in flight; spinner + brief
 *                 copy. Without this a 5-15 s pause looks like the app
 *                 froze, so the banner is doing the same job a "Saving…"
 *                 / "Reconnecting…" indicator does — explaining a delay,
 *                 not asking for input.
 *   - error:      summarisation failed and the loop dropped the oldest
 *                 half. Shown until the user dismisses it; the next
 *                 turn might otherwise feel like it lost memory for no
 *                 reason.
 *
 * Idle state renders nothing — same memoised shell so React doesn't
 * remount on each transition.
 *
 * No "warning" / "approaching threshold" state: the user can't act on
 * it (auto-compact handles it next turn) and ChatGPT / Claude.ai don't
 * surface that, either. Surfacing the metric without a paired action
 * adds anxiety; we'd rather stay silent until something demands
 * attention.
 *
 * Layout: a single-line pill, fit-content width, centered above the
 * composer. Roughly 24 px tall; deliberately quiet since this banner
 * is purely status, not a CTA. ChatGPT / Claude.ai's "regenerating…" / "saving…" affordance
 * is the precedent — it earns just the space the words need.
 */
export const CompactionBanner = memo(function CompactionBanner({
    state,
    error,
    onDismiss,
}: CompactionBannerProps) {
    if (!state || state === "idle") {
        return null;
    }

    if (state === "compacting") {
        return (
            <div className="mb-1.5 flex justify-center">
                <div
                    className="inline-flex items-center gap-1.5 rounded-full border border-accent/25 bg-panel-warm px-2.5 py-1 shadow-[var(--shadow-soft)]"
                    role="status"
                    aria-live="polite"
                >
                    <Loader2 className="size-3 shrink-0 animate-spin text-accent" strokeWidth={2.5} />
                    <span className="text-[11px] leading-none text-muted">
                        Compacting conversation
                    </span>
                </div>
            </div>
        );
    }

    // error
    return (
        <div className="mb-1.5 flex justify-center">
            <div
                className="inline-flex items-center gap-1.5 rounded-full border border-warning-border bg-panel px-2.5 py-1"
                role="alert"
            >
                <AlertTriangle className="size-3 shrink-0 text-warning" strokeWidth={2.5} />
                <span className="text-[11px] leading-none text-warning">
                    {error || "Older messages truncated; summariser unavailable."}
                </span>
                {onDismiss && (
                    <button
                        type="button"
                        className="ml-0.5 rounded text-warning/70 hover:text-warning"
                        onClick={onDismiss}
                        aria-label="Dismiss"
                    >
                        <X className="size-3" strokeWidth={2.5} />
                    </button>
                )}
            </div>
        </div>
    );
});
