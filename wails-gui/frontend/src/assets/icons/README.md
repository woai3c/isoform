# icons

Custom SVG icon components for the Wails GUI desktop app.

## Overview

This directory contains hand-crafted React SVG icon components used throughout the desktop UI. Icons are inline SVG, accept standard props for size and color, and integrate naturally with Tailwind's `text-*` utilities via `currentColor`.

## Shared Interface

`IconProps` is defined in `CollapseIcon.tsx` and re-imported by components that need it:

```typescript
export interface IconProps {
    className?: string;
    color?: string;           // defaults to 'currentColor'
    variant?: 'stroke' | 'fill';  // defaults to 'stroke'
    size?: number;            // defaults to 16
}
```

`LogoIcon` and `LogoWithFontIcon` have their own minimal prop types (see per-component docs below).

## Components

### `CollapseIcon`

Two-panel layout icon for the sidebar collapse action — left panel outlined, right panel filled (in `fill` variant), with a left-pointing arrow.

- **viewBox:** `20x20`
- **Props:** `IconProps` (`size` defaults to `16`)
- **Variants:**
  - `stroke` — outline-only; both panels share the same stroke color
  - `fill` — right panel filled with `currentColor`; arrow renders in `var(--panel, white)` to contrast against the fill

### `ExpandIcon`

Mirror of `CollapseIcon` — left panel filled (in `fill` variant), right panel outlined, with a right-pointing arrow.

- **viewBox:** `20x20`
- **Props:** `IconProps` (`size` defaults to `16`)
- **Variants:** same `stroke`/`fill` behavior as `CollapseIcon`

### `ChatIcon`

Speech bubble icon with a `+` indicator, used for new-chat actions.

- **viewBox:** `24x24`
- **Props:** `className?`, `color?` (default `'currentColor'`), `size?` (default `24`)

### `LogoIcon`

The Isoform mark (symbol only, no wordmark) uses Patina paths with `fill="var(--color-accent)"` directly. It is decorative by default; `label` supplies a translated accessible name when needed.

- **viewBox:** `19x19`
- **Props:** `className?`, `size?: number | string`, `label?`, `blink?`, `eyeClassName?`

### `SearchIcon`

Magnifying-glass search icon used in the command palette and search inputs.

- **viewBox:** `24x24`
- **Props:** `className?`, `color?` (default `'currentColor'`), `size?` (default `24`) — no `variant` prop

### `AutomationIcon`

Sidebar Library entry icon for the Automation (scheduled) page.

- **viewBox:** `24x24`
- **Props:** `IconProps` (`size` defaults to `24`) — `stroke` only, follows `currentColor`

### `HandoffIcon`

Sidebar Library entry icon for the Handoffs page, and the leading icon for handoff items in the Recent list.

- **viewBox:** `24x24`
- **Props:** `IconProps` (`size` defaults to `24`) — `stroke` only, follows `currentColor`

### `MeetingIcon`

Leading icon for a meeting event row in the Recent list (a meeting that produced multiple artifacts).

- **viewBox:** `20x20`
- **Props:** `IconProps` (`size` defaults to `20`) — `stroke` only, follows `currentColor`

### `TranscriptIcon`

Leading icon for transcript items in the Recent list.

- **viewBox:** `20x20`
- **Props:** `IconProps` (`size` defaults to `20`) — `stroke` only, follows `currentColor`

### `LogoWithFontIcon`

The Isoform lockup combines `LogoIcon` and the translated wordmark, both in the accent color.

- **viewBox:** `90x19`
- **Props:** `className?`, `height?` (default `19`), `blinkOnHover?`; width is derived automatically as `(90 / 19) * height`

## Usage

```tsx
import { CollapseIcon } from '@/assets/icons/CollapseIcon';
import { ExpandIcon }   from '@/assets/icons/ExpandIcon';
import { ChatIcon }     from '@/assets/icons/ChatIcon';
import { LogoIcon }     from '@/assets/icons/LogoIcon';
import { SearchIcon }       from '@/assets/icons/SearchIcon';
import { LogoWithFontIcon } from '@/assets/icons/LogoWithFontIcon';

// Sidebar toggle buttons
<CollapseIcon variant="fill" size={20} />
<ExpandIcon   variant="fill" size={20} />

// Functional UI icons
<ChatIcon className="text-muted" size={20} />

// Brand marks
<LogoIcon size={32} />
<LogoWithFontIcon height={24} />
```

## Notes

- `CollapseIcon` and `ExpandIcon` `fill` variant: the directional arrow is colored `var(--panel, white)`. Override the `--panel` CSS variable when the panel background is not white.
- `LogoWithFontIcon` and `LogoIcon` fill directly from `var(--color-accent)`; symbol and wordmark stay one color.
- `IconProps` is the single source of truth for the shared prop interface — import it from `CollapseIcon.tsx` rather than re-declaring it.

### `TopicIcon`

A tag with a punched hole: a topic pinned to the sidebar (RC 1). Same grid and stroke as `ChatBubbleIcon`.

- **viewBox:** `20x20`
- **Props:** `IconProps` (`size` defaults to `24`)
