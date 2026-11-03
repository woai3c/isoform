# ContextMenu

A right-click menu (min 160px, 8px radius, `color-border`, `shadow-card`) for row-level actions.

**Parts** `ContextMenu`, `ContextMenuTrigger`, `ContextMenuContent`, `ContextMenuItem` (13px, 6×8px padding; highlight ink at 6%; `destructive` → `color-error`), `ContextMenuSeparator`.

**Consumer provides** the trigger region and items. Offer the same actions elsewhere too — a context menu is never the only way in.

Source: `desktop/wails-gui/frontend/src/components/ui/context-menu.tsx`. Static rendition.
