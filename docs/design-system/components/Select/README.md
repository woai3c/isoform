# Select

A Radix select: a 36px trigger (8px radius, `color-border`, 13px) and a content list with checkmark indicators and scroll buttons.

**Parts** `Select`, `SelectGroup`, `SelectValue`, `SelectTrigger` (hover border `color-accent-2` at 40%; placeholder `color-muted`), `SelectContent` (12px radius, `color-border-strong`), `SelectLabel` (12px semibold uppercase `color-muted`), `SelectItem` (12.5px, 32px left inset for the check, `color-surface-active` hover), `SelectSeparator`.

**Consumer provides** `value` / `onValueChange`, a placeholder, and the items. Use `Combobox` instead when the list needs search or has more than a dozen options.

Source: `desktop/wails-gui/frontend/src/components/ui/select.tsx`. Static rendition.
