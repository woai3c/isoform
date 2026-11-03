import { cn } from "@/lib/utils";

export interface DebugViewOption<T extends string> {
    id: T;
    label: string;
}

export interface DebugViewGroup<T extends string> {
    id: string;
    label: string;
    views: readonly DebugViewOption<T>[];
}

interface DebugViewSwitcherProps<T extends string> {
    ariaLabel: string;
    groups: readonly DebugViewGroup<T>[];
    value: T;
    onValueChange: (value: T) => void;
}

export function DebugViewSwitcher<T extends string>({
    ariaLabel,
    groups,
    value,
    onValueChange,
}: DebugViewSwitcherProps<T>) {
    const activeGroup =
        groups.find((group) => group.views.some((view) => view.id === value)) ??
        groups[0];

    if (!activeGroup) return null;

    return (
        <div className="min-w-0 border border-border">
            <div
                role="tablist"
                aria-label={`${ariaLabel} groups`}
                className="grid min-w-0 border-b border-border bg-panel-warm"
                style={{ gridTemplateColumns: `repeat(${groups.length}, minmax(0, 1fr))` }}
            >
                {groups.map((group) => {
                    const selected = group.id === activeGroup.id;
                    return (
                        <button
                            key={group.id}
                            type="button"
                            role="tab"
                            aria-selected={selected}
                            className={cn(
                                "min-h-10 min-w-0 break-normal border-r border-border px-2 py-2 text-[10px] font-semibold leading-3 last:border-r-0 lg:text-[11px]",
                                selected
                                    ? "bg-ink text-background"
                                    : "text-muted hover:bg-ink/[0.03] hover:text-ink",
                            )}
                            onClick={() => {
                                const firstView = group.views[0];
                                if (!selected && firstView) onValueChange(firstView.id);
                            }}
                        >
                            {group.label}
                        </button>
                    );
                })}
            </div>

            <div
                role="tablist"
                aria-label={`${activeGroup.label} views`}
                className="grid min-w-0 items-stretch px-1"
                style={{
                    gridTemplateColumns:
                        "repeat(auto-fit, minmax(96px, 1fr))",
                }}
            >
                {activeGroup.views.map((view) => {
                    const selected = view.id === value;
                    return (
                        <button
                            key={view.id}
                            type="button"
                            role="tab"
                            aria-selected={selected}
                            className={cn(
                                "relative min-h-11 min-w-0 break-normal px-1 py-2 text-[10px] font-medium leading-3 lg:px-2 lg:text-[11px]",
                                selected
                                    ? "text-ink after:absolute after:inset-x-2 after:bottom-0 after:h-0.5 after:bg-ink"
                                    : "text-muted hover:text-ink",
                            )}
                            onClick={() => onValueChange(view.id)}
                        >
                            {view.label}
                        </button>
                    );
                })}
            </div>
        </div>
    );
}
