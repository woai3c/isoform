import { useCallback, useEffect, useState } from 'react';
import {
    LoadAllSidebarGroups,
    CreateSidebarGroup,
    RenameSidebarGroup as RenameSidebarGroupBackend,
    DeleteSidebarGroup as DeleteSidebarGroupBackend,
    ReorderSidebarGroups as ReorderSidebarGroupsBackend,
    SetSidebarGroupCollapsed as SetSidebarGroupCollapsedBackend,
    AddItemToSidebarGroup as AddItemToSidebarGroupBackend,
    RemoveItemFromSidebarGroup as RemoveItemFromSidebarGroupBackend,
    RemoveGroupItemsByRef as RemoveGroupItemsByRefBackend,
    ReorderSidebarGroupItems as ReorderSidebarGroupItemsBackend,
} from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import type { SidebarGroup, SidebarGroupItem } from '@/types';

type GroupItemType = SidebarGroupItem['itemType'];

export function useSidebarGroups() {
    const [groups, setGroups] = useState<SidebarGroup[]>([]);
    const [loading, setLoading] = useState(true);

    const loadGroups = useCallback(async () => {
        const data = await LoadAllSidebarGroups();
        setGroups((data ?? []) as SidebarGroup[]);
    }, []);

    // Load all groups on mount and when the backend signals changes.
    useEffect(() => {
        loadGroups()
            .catch((err) => {
                console.error('[sidebar-groups] load failed:', err);
            })
            .finally(() => setLoading(false));

        const unsub = EventsOn("sidebar:groups-changed", () => {
            loadGroups().catch((err) => {
                console.error('[sidebar-groups] reload failed:', err);
            });
        });
        return () => unsub();
    }, [loadGroups]);

    const createGroup = useCallback(async (name: string): Promise<SidebarGroup | null> => {
        try {
            const group = await CreateSidebarGroup(name);
            setGroups((prev) => [...prev, group as SidebarGroup]);
            return group as SidebarGroup;
        } catch (err) {
            console.error('[sidebar-groups] create failed:', err);
            return null;
        }
    }, []);

    const renameGroup = useCallback(async (id: string, name: string) => {
        // Optimistic update.
        setGroups((prev) =>
            prev.map((g) => (g.id === id ? { ...g, name } : g)),
        );
        try {
            await RenameSidebarGroupBackend(id, name);
        } catch (err) {
            console.error('[sidebar-groups] rename failed:', err);
            // Reload to get consistent state.
            await loadGroups();
        }
    }, [loadGroups]);

    const deleteGroup = useCallback(async (id: string) => {
        // Optimistic update.
        setGroups((prev) => prev.filter((g) => g.id !== id));
        try {
            await DeleteSidebarGroupBackend(id);
        } catch (err) {
            console.error('[sidebar-groups] delete failed:', err);
            await loadGroups();
        }
    }, [loadGroups]);

    const reorderGroups = useCallback(async (ids: string[]) => {
        // Optimistic update: reorder based on ids array.
        setGroups((prev) => {
            const map = new Map(prev.map((g) => [g.id, g]));
            return ids
                .map((id, i) => {
                    const g = map.get(id);
                    return g ? { ...g, sortOrder: i } : null;
                })
                .filter((g): g is SidebarGroup => g !== null);
        });
        try {
            await ReorderSidebarGroupsBackend(ids);
        } catch (err) {
            console.error('[sidebar-groups] reorder groups failed:', err);
            await loadGroups();
        }
    }, [loadGroups]);

    const setGroupCollapsed = useCallback(async (id: string, collapsed: boolean) => {
        setGroups((prev) =>
            prev.map((g) => (g.id === id ? { ...g, collapsed } : g)),
        );
        try {
            await SetSidebarGroupCollapsedBackend(id, collapsed);
        } catch (err) {
            console.error('[sidebar-groups] set collapsed failed:', err);
        }
    }, []);

    const addItemToGroup = useCallback(
        async (groupId: string, itemType: GroupItemType, refId: string): Promise<SidebarGroupItem | null> => {
            try {
                const item = await AddItemToSidebarGroupBackend(groupId, itemType, refId);
                setGroups((prev) =>
                    prev.map((g) => {
                        if (g.id !== groupId) return g;
                        // Avoid duplicates (backend returns existing if duplicate).
                        const exists = g.items.some((i) => i.id === item.id);
                        if (exists) return g;
                        return { ...g, items: [...g.items, item as SidebarGroupItem] };
                    }),
                );
                return item as SidebarGroupItem;
            } catch (err) {
                console.error('[sidebar-groups] add item failed:', err);
                return null;
            }
        },
        [],
    );

    const removeItemFromGroup = useCallback(async (groupId: string, itemId: string) => {
        // Optimistic update.
        setGroups((prev) =>
            prev.map((g) => {
                if (g.id !== groupId) return g;
                return { ...g, items: g.items.filter((i) => i.id !== itemId) };
            }),
        );
        try {
            await RemoveItemFromSidebarGroupBackend(groupId, itemId);
        } catch (err) {
            console.error('[sidebar-groups] remove item failed:', err);
            await loadGroups();
        }
    }, [loadGroups]);

    const removeItemsByRef = useCallback(async (itemType: GroupItemType, refId: string) => {
        // Optimistic update: remove from all groups.
        setGroups((prev) =>
            prev.map((g) => ({
                ...g,
                items: g.items.filter((i) => !(i.itemType === itemType && i.refId === refId)),
            })),
        );
        try {
            await RemoveGroupItemsByRefBackend(itemType, refId);
        } catch (err) {
            console.error('[sidebar-groups] remove items by ref failed:', err);
        }
    }, []);

    const moveItemBetweenGroups = useCallback(
        async (sourceGroupId: string, targetGroupId: string, itemId: string, targetIndex?: number) => {
            let itemType: GroupItemType = 'chat';
            let refId = '';

            // Optimistic update: move item from source to target group.
            setGroups((prev) => {
                const sourceGroup = prev.find((g) => g.id === sourceGroupId);
                const item = sourceGroup?.items.find((i) => i.id === itemId);
                if (!item) return prev;

                itemType = item.itemType;
                refId = item.refId;

                return prev.map((g) => {
                    if (g.id === sourceGroupId) {
                        return { ...g, items: g.items.filter((i) => i.id !== itemId) };
                    }
                    if (g.id === targetGroupId) {
                        const newItems = [...g.items];
                        newItems.splice(targetIndex ?? newItems.length, 0, { ...item, groupId: targetGroupId });
                        return { ...g, items: newItems };
                    }
                    return g;
                });
            });

            if (!refId) return;

            try {
                await RemoveItemFromSidebarGroupBackend(sourceGroupId, itemId);
                try {
                    await AddItemToSidebarGroupBackend(targetGroupId, itemType, refId);
                } catch {
                    // Add to target failed; restore item to source group.
                    await AddItemToSidebarGroupBackend(sourceGroupId, itemType, refId).catch(() => {});
                }
                await loadGroups();
            } catch (err) {
                console.error('[sidebar-groups] move item failed:', err);
                await loadGroups();
            }
        },
        [loadGroups],
    );

    const reorderGroupItems = useCallback(async (groupId: string, itemIds: string[]) => {
        // Optimistic update.
        setGroups((prev) =>
            prev.map((g) => {
                if (g.id !== groupId) return g;
                const itemMap = new Map(g.items.map((i) => [i.id, i]));
                const reordered = itemIds
                    .map((id, idx) => {
                        const item = itemMap.get(id);
                        return item ? { ...item, sortOrder: idx } : null;
                    })
                    .filter((i): i is SidebarGroupItem => i !== null);
                return { ...g, items: reordered };
            }),
        );
        try {
            await ReorderSidebarGroupItemsBackend(groupId, itemIds);
        } catch (err) {
            console.error('[sidebar-groups] reorder items failed:', err);
            await loadGroups();
        }
    }, [loadGroups]);

    return {
        groups,
        loading,
        createGroup,
        renameGroup,
        deleteGroup,
        reorderGroups,
        setGroupCollapsed,
        addItemToGroup,
        removeItemFromGroup,
        removeItemsByRef,
        moveItemBetweenGroups,
        reorderGroupItems,
    };
}
