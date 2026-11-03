# DropdownMenu

An action menu (min 12rem, 12px radius, `color-border-strong`, 4px padding) with optional labels, separators, sub-menus and destructive items.

**Parts** `DropdownMenu`, `DropdownMenuTrigger`, `DropdownMenuContent`, `DropdownMenuItem` (12.5px, 8×12px padding, 6px radius, 14px icons, `color-surface-active` highlight; `destructive` → `color-error` on `color-error-bg`), `DropdownMenuLabel` (12px medium `color-muted`), `DropdownMenuSeparator`, `DropdownMenuSub`, `DropdownMenuSubTrigger`, `DropdownMenuSubContent`.

**Consumer provides** items as verb-first commands with a leading lucide icon; put destructive items last, after a separator.

Source: `desktop/wails-gui/frontend/src/components/ui/dropdown-menu.tsx`. Static rendition.
