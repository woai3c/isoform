# Command

The command palette: a 520px cmdk list 20% from the top over a blurred ink-at-40% scrim, for keyboard-driven search and navigation.

**Parts** `Command`, `CommandDialog`, `CommandInput` (44px, 14px, leading search icon), `CommandList` (max 300px), `CommandEmpty`, `CommandGroup` (heading 11px medium uppercase, wide tracking, `color-muted`), `CommandSeparator`, `CommandItem` (14px, 8px padding, 8px radius; selected = `color-surface-active`, 120ms ease-out; no left-edge bar).

**Consumer provides** groups of destinations (nouns) and commands (verb + object); show shortcuts right-aligned in `color-subtle`.

**In the app** Search and commands (`components/search-dialog.tsx`, RC 2) widens the panel to 680px (`contentClassName`) because each row carries a meta line, and asks its body before Escape closes (`onEscapeKeyDown`: the first Escape clears the query).

Source: `desktop/wails-gui/frontend/src/components/ui/command.tsx`. Static rendition.
