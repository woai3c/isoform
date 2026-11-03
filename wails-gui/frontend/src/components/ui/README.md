# UI Components

Primitive UI component library for the Wails GUI frontend. These are low-level, reusable building blocks styled with TailwindCSS and backed by [Radix UI](https://www.radix-ui.com/) primitives.

## Overview

Each component wraps a Radix UI primitive (or is built from scratch) and applies the application's design tokens — `bg-panel`, `border-border`, `text-ink`, `text-muted`, `text-accent-2`, etc. — so that all UI elements share a consistent look and feel.

## Components

| File | Exports | Description |
|---|---|---|
| `avatar.tsx` | `Avatar` | Circular avatar with image + initials fallback. Sizes: `xs`, `sm`, `md` (default), `lg`. |
| `calendar.tsx` | `Calendar` | Month-view date picker built on `react-day-picker`. Supports single date, range, and multi-select modes. Styled with design tokens and chevron navigation icons. |
| `button.tsx` | `Button`, `buttonVariants` | Button with seven variants (`default`, `secondary`, `connect`, `ghost`, `outline`, `destructive`, `link`) and four sizes (`default`, `sm`, `lg`, `icon`). Supports `asChild` for rendering as any element via Radix `Slot`. |
| `card.tsx` | `Card`, `CardHeader`, `CardTitle`, `CardDescription`, `CardContent` | Bordered panel container with structured sub-components for headers and content. `CardHeader`/`CardContent` use generous `p-10`/`px-10 pb-12` padding for hero sections; `CardTitle` renders in serif font at 32px — designed for the product website landing page, not the main app UI. |
| `checkbox.tsx` | `Checkbox` | Accessible checkbox backed by Radix Checkbox. Checked state uses `accent-2` fill; supports disabled and focus-visible states. |
| `collapse-panel.tsx` | `CollapsePanel` | Animated expand/collapse wrapper using CSS `grid-template-rows` transition. Content stays mounted while collapsed, preserving internal state. Used by `processing-block` and `composer-suggestions`. |
| `collapsible.tsx` | `Collapsible`, `CollapsibleTrigger`, `CollapsibleContent` | Thin re-export of Radix Collapsible for show/hide sections. |
| `command.tsx` | `Command`, `CommandDialog`, `CommandInput`, `CommandList`, `CommandEmpty`, `CommandGroup`, `CommandSeparator`, `CommandItem` | Command palette / keyboard-driven search list built on `cmdk`. `CommandDialog` wraps the list in a centered modal overlay; used by the application's command palette feature. |
| `combobox.tsx` | `Combobox` | Searchable select built on Radix Popover with a custom search input and keyboard navigation. Accepts `options`, `value`, `onValueChange`. Supports optional portal rendering, custom value entry (`allowCustom`), custom option rendering (`renderOption`), and automatic virtualised rendering for lists over 50 items via `react-virtuoso`. |
| `context-menu.tsx` | `ContextMenu`, `ContextMenuTrigger`, `ContextMenuContent`, `ContextMenuItem`, `ContextMenuSeparator` | Right-click context menu. Items accept a `destructive` prop for error-state styling. |
| `popover.tsx` | `Popover`, `PopoverTrigger`, `PopoverContent`, `PopoverAnchor` | Floating content panel backed by Radix Popover. Renders in a portal with enter/exit animations and configurable alignment and side offset. Sets `--wails-draggable: no-drag` to prevent drag interference inside Wails windows. Used internally by `combobox.tsx`. |
| `dialog.tsx` | `Dialog`, `DialogTrigger`, `DialogContent`, `DialogHeader`, `DialogTitle`, `DialogDescription`, `DialogClose`, `DialogCloseButton`, `DialogOverlay`, `DialogPortal` | Modal dialog with blurred overlay, centered content panel, and a pre-built close button. |
| `dropdown-menu.tsx` | `DropdownMenu`, `DropdownMenuTrigger`, `DropdownMenuContent`, `DropdownMenuItem`, `DropdownMenuSeparator`, `DropdownMenuLabel`, `DropdownMenuSub`, `DropdownMenuSubTrigger`, `DropdownMenuSubContent` | Dropdown menu with support for nested sub-menus and destructive items. |
| `scroll-area.tsx` | `ScrollArea`, `ScrollBar` | Scroll container with a styled custom scrollbar (vertical and horizontal). |
| `select.tsx` | `Select`, `SelectGroup`, `SelectValue`, `SelectTrigger`, `SelectContent`, `SelectLabel`, `SelectItem` | Accessible select dropdown with scroll buttons and checkmark indicators. |
| `sheet.tsx` | `Sheet`, `SheetTrigger`, `SheetContent`, `SheetHeader`, `SheetTitle`, `SheetDescription`, `SheetClose`, `SheetCloseButton` | Slide-in side panel (right edge) built on Radix Dialog. Animates in/out via CSS translate. Items destructure the same way as `dialog.tsx`. |
| `switch.tsx` | `Switch` | Toggle switch backed by Radix Switch. Checked state uses `ink` fill with a white thumb; supports disabled and focus-visible states. |
| `textarea.tsx` | `Textarea` | Resizable textarea with consistent border and focus styling. |

## Design Patterns

- **Radix UI primitives** — accessibility (keyboard nav, ARIA, focus management) is handled by the underlying primitives.
- **`cn()` utility** — all components use `cn` from `@/lib/utils` (a `clsx` + `tailwind-merge` wrapper) so callers can pass additional `className` props without conflicts.
- **`cva` variants** — `Button` uses `class-variance-authority` to define typed variant/size combinations.
- **Design tokens** — styling references CSS variables (`--shadow-button`, `--shadow-card`, etc.) and semantic Tailwind classes (`bg-panel`, `text-ink`, `border-border`) rather than hard-coded values.
- **`forwardRef`** — most components forward refs for integration with animation libraries or direct DOM access. (Built-in components like `Avatar`, `Combobox`, and `CollapsePanel` use plain function components and do not forward refs.)

## Dependencies

| Package | Purpose |
|---|---|
| `@radix-ui/react-checkbox` | Checkbox primitive |
| `@radix-ui/react-popover` | Popover primitive (used by `popover.tsx` and `combobox.tsx`) |
| `@radix-ui/react-collapsible` | Collapsible primitive |
| `@radix-ui/react-context-menu` | Context menu primitive |
| `@radix-ui/react-dialog` | Dialog/modal primitive |
| `@radix-ui/react-dropdown-menu` | Dropdown menu primitive |
| `react-day-picker` | Calendar/date picker used by `calendar.tsx` |
| `@radix-ui/react-scroll-area` | Scroll area primitive |
| `@radix-ui/react-select` | Select primitive |
| `@radix-ui/react-slot` | `asChild` pattern for Button |
| `@radix-ui/react-switch` | Switch primitive |
| `cmdk` | Command palette / search list used by `command.tsx` |
| `react-virtuoso` | Virtualised list rendering used by `combobox.tsx` for large option sets (>50 items) |
| `class-variance-authority` | Typed variant API (`cva`) |
| `lucide-react` | Icons |

## Usage

Import directly from each file:

```tsx
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogCloseButton } from '@/components/ui/dialog';
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select';
import { Combobox } from '@/components/ui/combobox';
import { Avatar } from '@/components/ui/avatar';

// Button variants
<Button variant="default">Save</Button>
<Button variant="destructive">Delete</Button>
<Button variant="outline" size="sm">Cancel</Button>
<Button variant="ghost" size="icon"><IconComponent /></Button>

// Dialog
<Dialog>
  <DialogTrigger asChild><Button>Open</Button></DialogTrigger>
  <DialogContent>
    <DialogHeader>
      <DialogTitle>Title</DialogTitle>
      <DialogCloseButton />
    </DialogHeader>
    {/* body */}
  </DialogContent>
</Dialog>

// Combobox (searchable select)
<Combobox
  options={[{ value: 'a', label: 'Option A' }]}
  value={selected}
  onValueChange={setSelected}
  placeholder="Choose..."
/>

// Avatar with fallback initials
<Avatar src={user.avatarUrl} fallback={user.name} size="sm" />
```
