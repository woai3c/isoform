import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown, MoreHorizontal, Pencil, Trash2, X } from 'lucide-react';
import { useDroppable } from '@dnd-kit/core';
import {
    SortableContext,
    useSortable,
    verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import {
    DropdownMenu,
    DropdownMenuTrigger,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu';
import type { SidebarGroup, SidebarGroupItem } from '@/types';
import { CollapsePanel } from '@/components/ui/collapse-panel';

// --- Sortable item within a group ---

interface SortableGroupItemProps {
    item: SidebarGroupItem;
    groupId: string;
    label: string;
    icon?: React.ReactNode;
    isActive: boolean;
    onClickItem: (item: SidebarGroupItem) => void;
    onRemoveItem: (groupId: string, itemId: string) => void;
}

function SortableGroupItem({ item, groupId, label, isActive, onClickItem, onRemoveItem }: SortableGroupItemProps) {
    const {
        attributes,
        listeners,
        setNodeRef,
        transform,
        transition,
        isDragging,
    } = useSortable({ id: item.id, data: { type: 'item', groupId } });

    const style = {
        transform: CSS.Translate.toString(transform),
        transition,
    };

    // Match chat-session item styling so active custom-group items read as
    // "currently open" the same way chat rows do: filled bg, bumped weight,
    // plus the sidebar-active-in entrance animation.
    const stateClass = isActive
        ? 'text-ink font-medium bg-ink/[0.06] sidebar-active-in'
        : 'text-ink hover:bg-ink/[0.06]';

    return (
        <div
            ref={setNodeRef}
            style={style}
            {...attributes}
            {...listeners}
            className={`group/item flex items-center h-8 px-2 -mx-2 rounded-lg text-[14px] font-normal transition-colors duration-[120ms] ease-[cubic-bezier(0.16,1,0.3,1)] cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-2/40 ${stateClass} ${isDragging ? 'opacity-50 z-50' : ''}`}
            onClick={() => onClickItem(item)}
            onKeyDown={(event) => {
                // Declared after the dnd listeners so Enter and Space open
                // the item instead of starting dnd-kit's keyboard drag.
                // Only the row itself: keys aimed at the remove button
                // belong to it.
                if (event.target !== event.currentTarget) return;
                if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault();
                    event.stopPropagation();
                    onClickItem(item);
                }
            }}
        >
            <span className="flex-1 min-w-0 truncate">
                {label}
            </span>
            <button
                className="flex items-center justify-center w-5 h-5 rounded text-muted/80 hover:text-ink hover:bg-ink/10 opacity-0 group-hover/item:opacity-100 focus-visible:opacity-100 transition-all cursor-pointer shrink-0 ml-1"
                onClick={(e) => {
                    e.stopPropagation();
                    onRemoveItem(groupId, item.id);
                }}
                onKeyDown={(e) => e.stopPropagation()}
                title="Remove from group"
            >
                <X className="h-3 w-3" />
            </button>
        </div>
    );
}

// --- Drop zone for empty groups (accepts cross-group item drops) ---

function EmptyGroupDropZone({ groupId }: { groupId: string }) {
    const { setNodeRef, isOver } = useDroppable({
        id: `drop-zone-${groupId}`,
        data: { type: 'group-drop-zone', groupId },
    });

    return (
        <div ref={setNodeRef} className={`px-3 rounded-md transition-colors ${isOver ? 'py-2 bg-accent/10' : 'py-1.5'}`}>
            {isOver ? (
                <p className="text-[11px] text-accent/70">Drop here</p>
            ) : (
                <p className="text-[11px] text-muted/50">
                    No items yet. Right-click a chat to add here.
                </p>
            )}
        </div>
    );
}

// --- Group section (itself sortable at the group level) ---

interface SidebarGroupSectionProps {
    group: SidebarGroup;
    onRename: (id: string, name: string) => void;
    onDelete: (id: string) => void;
    onToggleCollapse: (id: string, collapsed: boolean) => void;
    onRemoveItem: (groupId: string, itemId: string) => void;
    onClickItem: (item: SidebarGroupItem) => void;
    /** Resolve a group item to its display label. */
    resolveItemLabel: (item: SidebarGroupItem) => string;
    /** Resolve a group item to its display icon. */
    resolveItemIcon?: (item: SidebarGroupItem) => React.ReactNode;
    /** Whether this item points at the currently-open session/etc. */
    isItemActive?: (item: SidebarGroupItem) => boolean;
    /** Start in rename mode (used when a group is freshly created). */
    initialRenaming?: boolean;
}

export function SidebarGroupSection({
    group,
    onRename,
    onDelete,
    onToggleCollapse,
    onRemoveItem,
    onClickItem,
    resolveItemLabel,
    resolveItemIcon,
    isItemActive,
    initialRenaming = false,
}: SidebarGroupSectionProps) {
    const { t } = useTranslation('app');
    const {
        attributes,
        listeners,
        setNodeRef,
        transform,
        transition,
        isDragging,
    } = useSortable({ id: group.id, data: { type: 'group' } });

    const style = {
        transform: CSS.Translate.toString(transform),
        transition,
    };

    const [isRenaming, setIsRenaming] = useState(initialRenaming);
    const [renameValue, setRenameValue] = useState(group.name);
    const inputRef = useRef<HTMLInputElement>(null);

    useEffect(() => {
        if (isRenaming) {
            inputRef.current?.focus();
            inputRef.current?.select();
        }
    }, [isRenaming]);

    const commitRename = useCallback(() => {
        const trimmed = renameValue.trim();
        if (trimmed && trimmed !== group.name) {
            onRename(group.id, trimmed);
        } else {
            setRenameValue(group.name);
        }
        setIsRenaming(false);
    }, [renameValue, group.name, group.id, onRename]);

    const itemIds = useMemo(
        () => group.items.map((i) => i.id),
        [group.items],
    );

    return (
        <div ref={setNodeRef} style={style} className={`w-full ${isDragging ? 'opacity-50 z-50' : ''}`}
            {...attributes}
            {...listeners}
        >
            {/* Group header. The fold toggle is a real button so it works
                from the keyboard; the menu stays its sibling because a button
                cannot nest inside a button. While renaming, the header is
                plain layout again. The section is a dnd-kit sortable node
                whose KeyboardSensor claims any Enter/Space keydown that
                reaches it (preventDefault, then drag), so every interactive
                child stops keydown propagation to keep native activation. */}
            {isRenaming ? (
                <div className="flex items-center px-4 py-1.5">
                    <input
                        ref={inputRef}
                        type="text"
                        className="min-w-0 bg-transparent text-[14px] font-normal text-ink outline-none border-b border-accent"
                        value={renameValue}
                        onChange={(e) => setRenameValue(e.target.value)}
                        onBlur={commitRename}
                        onClick={(e) => e.stopPropagation()}
                        onKeyDown={(e) => {
                            e.stopPropagation();
                            if (e.key === 'Enter') commitRename();
                            if (e.key === 'Escape') {
                                setRenameValue(group.name);
                                setIsRenaming(false);
                            }
                        }}
                    />
                </div>
            ) : (
                <div className="group/group-header flex items-center">
                    <button
                        type="button"
                        aria-expanded={!group.collapsed}
                        title={t('sidebar.toggleSection', { section: group.name })}
                        className="flex min-w-0 flex-1 cursor-pointer items-center py-1.5 pl-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-2/40"
                        onClick={() => onToggleCollapse(group.id, !group.collapsed)}
                        onKeyDown={(e) => e.stopPropagation()}
                    >
                        <span className="min-w-0 truncate text-[14px] font-normal text-muted group-hover/group-header:text-ink transition-colors duration-[var(--motion-duration-fast)] ease-[var(--motion-ease-out)] mr-1">
                            {group.name}
                        </span>
                        <ChevronDown
                            className={`h-3 w-3 shrink-0 text-muted group-hover/group-header:text-ink transition-transform duration-[var(--motion-duration-base)] ease-[var(--motion-ease-out)] ${group.collapsed ? '' : 'rotate-180'}`}
                        />
                    </button>
                    <div className="ml-1 pr-4 opacity-0 group-hover/group-header:opacity-100 focus-within:opacity-100 transition-opacity" onKeyDown={(e) => e.stopPropagation()}>
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                                <button className="flex cursor-pointer items-center justify-center rounded-md p-1 text-muted transition-colors hover:bg-ink/10 hover:text-ink">
                                    <MoreHorizontal className="h-3 w-3" />
                                </button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end" sideOffset={4}>
                                <DropdownMenuItem
                                    onClick={() => {
                                        setRenameValue(group.name);
                                        setIsRenaming(true);
                                    }}
                                >
                                    <Pencil className="h-3.5 w-3.5" />
                                    {t('sidebar.renameGroup')}
                                </DropdownMenuItem>
                                <DropdownMenuSeparator />
                                <DropdownMenuItem
                                    className="text-error focus:text-error"
                                    onClick={() => onDelete(group.id)}
                                >
                                    <Trash2 className="h-3.5 w-3.5" />
                                    {t('sidebar.deleteGroup')}
                                </DropdownMenuItem>
                            </DropdownMenuContent>
                        </DropdownMenu>
                    </div>
                </div>
            )}

            {/* Group items */}
            {/* The inset leaves room for a row's focus ring inside the clip. */}
            <CollapsePanel open={!group.collapsed} innerClassName="px-1 -mx-1 py-0.5 -my-0.5">
                <div className="px-4 pl-7 pb-1 flex flex-col gap-0.5">
                    {group.items.length === 0 ? (
                        <EmptyGroupDropZone groupId={group.id} />
                    ) : (
                        <SortableContext
                            items={itemIds}
                            strategy={verticalListSortingStrategy}
                        >
                            {group.items.map((item) => (
                                <SortableGroupItem
                                    key={item.id}
                                    item={item}
                                    groupId={group.id}
                                    label={resolveItemLabel(item)}
                                    icon={resolveItemIcon?.(item)}
                                    isActive={isItemActive?.(item) ?? false}
                                    onClickItem={onClickItem}
                                    onRemoveItem={onRemoveItem}
                                />
                            ))}
                        </SortableContext>
                    )}
                </div>
            </CollapsePanel>
        </div>
    );
}
