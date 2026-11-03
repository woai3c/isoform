import * as PopoverPrimitive from '@radix-ui/react-popover';
import { Check, ChevronDown } from 'lucide-react';
import { SearchIcon } from '@/assets/icons/SearchIcon';
import * as React from 'react';
import { Virtuoso } from 'react-virtuoso';
import { cn } from '@/lib/utils';

export interface ComboboxOption {
    value: string;
    label: string;
}

interface ComboboxProps {
    options: readonly ComboboxOption[];
    value: string;
    onValueChange: (value: string) => void;
    placeholder?: string;
    searchPlaceholder?: string;
    emptyMessage?: string;
    disabled?: boolean;
    className?: string;
    portal?: boolean;
    /** When true, allows typing a custom value not in the options list. */
    allowCustom?: boolean;
    /** Custom renderer for each option and the trigger label. */
    renderOption?: (option: ComboboxOption) => React.ReactNode;
}

/** Threshold above which virtualised rendering kicks in. */
const VIRTUAL_THRESHOLD = 50;

export function Combobox({
    options,
    value,
    onValueChange,
    placeholder = 'Select...',
    searchPlaceholder = 'Search...',
    emptyMessage = 'No results found.',
    disabled = false,
    className,
    portal = true,
    allowCustom = false,
    renderOption,
}: ComboboxProps) {
    const [open, setOpen] = React.useState(false);
    const [search, setSearch] = React.useState('');
    const [activeIndex, setActiveIndex] = React.useState(0);
    const inputRef = React.useRef<HTMLInputElement>(null);

    const selectedOption = React.useMemo(
        () => options.find((o) => o.value === value) ?? null,
        [options, value],
    );

    const selectedLabel = React.useMemo(
        () => selectedOption?.label ?? (allowCustom ? value : ''),
        [selectedOption, value, allowCustom],
    );

    // Filter options by search query
    const filtered = React.useMemo(() => {
        const q = search.trim().toLowerCase();
        if (!q) return options;
        return options.filter((o) => o.label.toLowerCase().includes(q));
    }, [options, search]);

    const showCustomItem =
        allowCustom &&
        search.trim() !== '' &&
        !options.some(
            (o) =>
                o.label.toLowerCase() === search.trim().toLowerCase() ||
                o.value === search.trim(),
        );

    // Items: optional custom entry + filtered options
    const items = React.useMemo(() => {
        const result: Array<
            { type: 'custom' } | { type: 'option'; option: ComboboxOption }
        > = [];
        if (showCustomItem) {
            result.push({ type: 'custom' });
        }
        for (const opt of filtered) {
            result.push({ type: 'option', option: opt });
        }
        return result;
    }, [filtered, showCustomItem]);

    // Reset active index on search change
    React.useEffect(() => {
        setActiveIndex(0);
    }, [search]);

    const selectItem = React.useCallback(
        (index: number) => {
            const item = items[index];
            if (!item) return;
            if (item.type === 'custom') {
                onValueChange(search.trim());
            } else {
                onValueChange(item.option.value);
            }
            setSearch('');
            setOpen(false);
        },
        [items, search, onValueChange],
    );

    const virtuosoRef = React.useRef<{ scrollToIndex: (opts: { index: number; align?: string }) => void } | null>(null);

    const handleKeyDown = React.useCallback(
        (e: React.KeyboardEvent) => {
            if (e.key === 'Escape') {
                // Stop the event here so Escape only dismisses the popover —
                // without this it bubbles to window and closes the surrounding
                // dialog (e.g. the settings modal) in the same keypress.
                e.preventDefault();
                e.stopPropagation();
                setOpen(false);
                return;
            }

            if (items.length === 0) return;

            if (e.key === 'ArrowDown') {
                e.preventDefault();
                setActiveIndex((i) => {
                    const next = Math.min(i + 1, items.length - 1);
                    virtuosoRef.current?.scrollToIndex({ index: next, align: 'auto' });
                    return next;
                });
            } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                setActiveIndex((i) => {
                    const next = Math.max(i - 1, 0);
                    virtuosoRef.current?.scrollToIndex({ index: next, align: 'auto' });
                    return next;
                });
            } else if (e.key === 'Enter') {
                e.preventDefault();
                selectItem(activeIndex);
            }
        },
        [items.length, activeIndex, selectItem],
    );

    // Focus input when popover opens
    React.useEffect(() => {
        if (open) {
            const t = setTimeout(() => inputRef.current?.focus(), 0);
            return () => clearTimeout(t);
        }
    }, [open]);

    const useVirtual = items.length > VIRTUAL_THRESHOLD;

    const renderItem = React.useCallback(
        (index: number) => {
            const item = items[index];
            const isActive = index === activeIndex;

            if (item.type === 'custom') {
                return (
                    <div
                        role="option"
                        aria-selected={isActive}
                        className={cn(
                            'relative flex w-full cursor-default items-center rounded-md py-1.5 pr-2 pl-8 text-sm select-none',
                            isActive && 'bg-accent-2/[0.06]',
                        )}
                        onPointerMove={() => setActiveIndex(index)}
                        onClick={() => selectItem(index)}
                    >
                        <span className="text-muted mr-1.5">Use:</span>
                        {search.trim()}
                    </div>
                );
            }

            const option = item.option;
            return (
                <div
                    role="option"
                    aria-selected={isActive}
                    className={cn(
                        'relative flex w-full cursor-default items-center rounded-md py-1.5 pr-2 pl-8 text-sm select-none',
                        isActive && 'bg-accent-2/[0.06]',
                    )}
                    onPointerMove={() => setActiveIndex(index)}
                    onClick={() => selectItem(index)}
                >
                    <span className="absolute left-2 flex h-3.5 w-3.5 items-center justify-center">
                        {option.value === value && (
                            <Check className="h-4 w-4" />
                        )}
                    </span>
                    {renderOption ? renderOption(option) : option.label}
                </div>
            );
        },
        [items, activeIndex, value, search, selectItem, renderOption],
    );

    const listContent = items.length === 0 ? (
        <div className="py-4 text-center text-[13px] text-muted">
            {emptyMessage}
        </div>
    ) : useVirtual ? (
        <Virtuoso
            ref={virtuosoRef as React.Ref<any>}
            style={{ height: Math.min(items.length * 32, 208) }}
            totalCount={items.length}
            itemContent={renderItem}
            className="p-1"
        />
    ) : (
        <div className="max-h-80 overflow-y-auto p-1">
            {items.map((_, i) => (
                <React.Fragment key={items[i].type === 'custom' ? '__custom__' : (items[i] as { type: 'option'; option: ComboboxOption }).option.value}>
                    {renderItem(i)}
                </React.Fragment>
            ))}
        </div>
    );

    const content = (
        <PopoverPrimitive.Content
            className="z-50 min-w-[var(--radix-popover-trigger-width)] rounded-lg border border-border bg-panel text-ink shadow-[var(--shadow-card)] data-[side=bottom]:translate-y-1 data-[side=top]:-translate-y-1"
            sideOffset={4}
            align="start"
            collisionPadding={8}
            onOpenAutoFocus={(e) => e.preventDefault()}
        >
            <div className="flex flex-col" onKeyDown={handleKeyDown}>
                <div className="flex items-center gap-2 border-b border-border px-2.5">
                    <SearchIcon className="h-3.5 w-3.5 shrink-0 opacity-40" />
                    <input
                        ref={inputRef}
                        placeholder={searchPlaceholder}
                        value={search}
                        onChange={(e) => setSearch(e.target.value)}
                        className="flex h-9 w-full bg-transparent text-[13px] text-ink outline-none placeholder:text-muted"
                    />
                </div>
                {listContent}
            </div>
        </PopoverPrimitive.Content>
    );

    return (
        <PopoverPrimitive.Root
            open={open}
            onOpenChange={(next) => {
                setOpen(next);
                if (!next) {
                    setSearch('');
                    setActiveIndex(0);
                }
            }}
        >
            <PopoverPrimitive.Trigger asChild disabled={disabled}>
                <button
                    role="combobox"
                    aria-expanded={open}
                    className={cn(
                        'flex h-9 w-full items-center justify-between gap-2 rounded-lg border border-border bg-panel px-2.5 py-1.5 text-[13px] text-ink transition-colors cursor-pointer hover:border-accent-2/40 focus:outline-none disabled:cursor-not-allowed disabled:opacity-50',
                        !value && 'text-muted',
                        className,
                    )}
                    disabled={disabled}
                >
                    <span className="truncate">
                        {value
                            ? renderOption
                                ? renderOption(
                                      selectedOption ?? {
                                          value,
                                          label: selectedLabel,
                                      },
                                  )
                                : selectedLabel
                            : placeholder}
                    </span>
                    <ChevronDown className="h-3.5 w-3.5 shrink-0 opacity-50" />
                </button>
            </PopoverPrimitive.Trigger>

            {portal ? (
                <PopoverPrimitive.Portal>{content}</PopoverPrimitive.Portal>
            ) : (
                content
            )}
        </PopoverPrimitive.Root>
    );
}
