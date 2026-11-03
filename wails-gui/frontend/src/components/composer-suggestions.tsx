import { useState } from "react";
import { RotateCw } from "lucide-react";

import { cn } from "@/lib/utils";
import { HandoffIcon } from "@/assets/icons/HandoffIcon";

interface ComposerSuggestionsProps {
    suggestions: string[];
    isLoading: boolean;
    isRefreshing: boolean;
    /**
     * When true, the suggestions smoothly collapse to zero height and fade
     * out — the component stays mounted so we animate instead of teleport.
     */
    collapsed?: boolean;
    onSelect: (prompt: string) => void;
    // TODO(debug-only): drop `onRefresh` when the debug refresh button is removed.
    onRefresh?: () => void;
    /** Active handoffs to surface as prioritized chips. */
    handoffs?: { id: string; title: string }[];
    /** Invoked when the handoff chip is clicked — opens the handoff reader. */
    onOpenHandoff?: (id: string) => void;
    className?: string;
}

const SKELETON_WIDTHS = ["w-40", "w-32", "w-48", "w-36", "w-44"] as const;

export function ComposerSuggestions({
    suggestions,
    isLoading,
    isRefreshing,
    collapsed = false,
    onSelect,
    onRefresh,
    handoffs = [],
    onOpenHandoff,
    className,
}: ComposerSuggestionsProps) {
    // TODO(debug-only): remove this block — only visible in dev builds.
    const showDebugRefresh =
        import.meta.env.DEV && typeof onRefresh === "function";
    const maxVisibleChips = 6;
    const visibleHandoffs = handoffs.slice(0, maxVisibleChips);
    // Chat K4 (C025): a suggestion is sent at once and leaves the row.
    const [used, setUsed] = useState<ReadonlySet<string>>(() => new Set());
    const visibleSuggestions = suggestions.filter((text) => !used.has(text)).slice(
        0,
        Math.max(0, maxVisibleChips - visibleHandoffs.length),
    );
    const hasContent =
        isLoading || visibleSuggestions.length > 0 || visibleHandoffs.length > 0;
    const isOpen = hasContent && !collapsed;

    const body =
        isLoading && visibleHandoffs.length === 0 ? (
            <div
                className="flex flex-wrap justify-center gap-2 pt-3 pb-1"
                aria-hidden
            >
                {SKELETON_WIDTHS.map((w, i) => (
                    <div
                        key={i}
                        className={cn(
                            "h-7 rounded-full bg-card-placeholder animate-pulse",
                            w,
                        )}
                    />
                ))}
            </div>
        ) : (
            <div
                className={cn(
                    "flex flex-wrap items-center justify-center gap-2 pt-3 pb-1 transition-opacity",
                    isRefreshing && "opacity-60",
                )}
            >
                {visibleHandoffs.map((handoff, i) => (
                    <button
                        key={handoff.id}
                        type="button"
                        onClick={() => onOpenHandoff?.(handoff.id)}
                        className={cn(
                            "suggestion-pill-in",
                            "inline-flex items-center gap-1.5 rounded-full border border-chip-border bg-panel px-3.5 py-1.5",
                            "text-[13px] font-normal text-ink/85",
                            "hover:border-border hover:bg-panel-warm hover:text-ink active:bg-card-placeholder",
                            "focus:outline-none focus-visible:ring-1 focus-visible:ring-border-focus",
                            "cursor-pointer transition-colors",
                        )}
                        style={{ animationDelay: `${i * 80}ms` }}
                        title={handoff.title}
                    >
                        <HandoffIcon
                            size={14}
                            className="shrink-0 text-muted"
                        />
                        <span className="truncate max-w-[240px]">
                            {handoff.title}
                        </span>
                    </button>
                ))}
                {visibleSuggestions.map((text, i) => (
                    <button
                        key={`${i}-${text}`}
                        type="button"
                        onClick={() => {
                            setUsed((prev) => new Set(prev).add(text));
                            onSelect(text);
                        }}
                        className={cn(
                            "suggestion-pill-in",
                            "rounded-full border border-chip-border bg-panel px-3.5 py-1.5",
                            "text-[13px] font-normal text-ink/85",
                            "hover:border-border hover:bg-panel-warm hover:text-ink active:bg-card-placeholder",
                            "focus:outline-none focus-visible:ring-1 focus-visible:ring-border-focus",
                            "cursor-pointer transition-colors",
                        )}
                        style={{
                            animationDelay: `${(visibleHandoffs.length + i) * 80}ms`,
                        }}
                        title={text}
                    >
                        {text}
                    </button>
                ))}
                {/* TODO(debug-only): remove this refresh button once validated. */}
                {showDebugRefresh && (
                    <button
                        type="button"
                        onClick={onRefresh}
                        disabled={isRefreshing}
                        className={cn(
                            "suggestion-pill-in",
                            "rounded-full border border-chip-border bg-panel p-2",
                            "text-muted",
                            "hover:bg-panel-warm hover:text-ink",
                            "focus:outline-none focus-visible:ring-1 focus-visible:ring-border-focus",
                            "disabled:cursor-not-allowed disabled:opacity-50",
                            "cursor-pointer transition-colors",
                        )}
                        style={{
                            animationDelay: `${
                                (visibleHandoffs.length +
                                    visibleSuggestions.length) *
                                80
                            }ms`,
                        }}
                        title="Regenerate suggestions (dev only)"
                        aria-label="Regenerate suggestions"
                    >
                        <RotateCw
                            className={cn(
                                "h-4 w-4",
                                isRefreshing && "animate-spin",
                            )}
                        />
                    </button>
                )}
            </div>
        );

    return (
        <div
            aria-hidden={!isOpen}
            className={cn(
                "grid transition-[grid-template-rows,opacity] duration-[var(--motion-duration-slow)] ease-[var(--motion-ease-in-out)]",
                className,
            )}
            style={{
                gridTemplateRows: isOpen ? "1fr" : "0fr",
                opacity: isOpen ? 1 : 0,
                pointerEvents: isOpen ? "auto" : "none",
            }}
        >
            <div className="min-h-0 overflow-hidden">{body}</div>
        </div>
    );
}
