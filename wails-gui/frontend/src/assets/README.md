# assets

Static assets for the Isoform desktop (wails-gui) frontend.

## Contents

### Icons (`icons/`)

Custom SVG icon components. These replace raster images and generic lucide-react icons
for brand and functional UI elements.

| File | Export(s) | Description |
|------|-----------|-------------|
| `CollapseIcon.tsx` | `CollapseIcon`, `IconProps` | Two-panel layout icon — right panel filled, left outlined, left-pointing arrow. Supports `stroke` / `fill` variants. |
| `ExpandIcon.tsx` | `ExpandIcon` | Mirror of `CollapseIcon` — left panel filled, right outlined, right-pointing arrow. |
| `ChatIcon.tsx` | `ChatIcon` | Chat bubble icon with a "+" badge, used in sidebar and search dialog. |
| `LogoIcon.tsx` | `LogoIcon` | Isoform mark only (19×19 viewBox). Used in the new-chat welcome screen. |
| `LogoWithFontIcon.tsx` | `LogoWithFontIcon` | Isoform mark + wordmark (90×19 viewBox). Used in the sidebar header. |
| `SearchIcon.tsx` | `SearchIcon` | Magnifying-glass search icon (24×24 viewBox). Uses `IconProps` without the `variant` field. |

## Icon API

### `IconProps` (exported from `CollapseIcon.tsx`)

Shared by `CollapseIcon`, `ExpandIcon`, and `ChatIcon`:

```ts
export interface IconProps {
  className?: string;
  color?: string;          // defaults to 'currentColor'
  variant?: 'stroke' | 'fill';  // defaults to 'stroke'
  size?: number;           // defaults to 16
}
```

The brand mark uses the supplied Brand violet paths, `fill="var(--color-accent)"` directly, so inherited text classes cannot recolor it. Inline marks never use app-icon tiles or texture.

### `LogoIconProps`

Used by `LogoIcon`:

```ts
interface LogoIconProps {
  className?: string;
  size?: number | string;
  label?: string; // translated accessible name; decorative when absent
  blink?: boolean;
  eyeClassName?: string;
}
```

### `LogoWithFontIconProps`

Used by `LogoWithFontIcon`. Width is derived automatically from `height` to preserve
the 90:19 aspect ratio:

```ts
interface LogoWithFontIconProps {
  className?: string;
  height?: number;  // defaults to 19
  blinkOnHover?: boolean;
}
```

## Usage

Import icon components:

```tsx
import { CollapseIcon } from '@/assets/icons/CollapseIcon';
import { ExpandIcon } from '@/assets/icons/ExpandIcon';
import { ChatIcon } from '@/assets/icons/ChatIcon';
import { LogoIcon } from '@/assets/icons/LogoIcon';
import { LogoWithFontIcon } from '@/assets/icons/LogoWithFontIcon';
import { SearchIcon } from '@/assets/icons/SearchIcon';

// Stroke (default) variant
<CollapseIcon />

// Fill variant with custom size
<ExpandIcon variant="fill" size={20} />

// With Tailwind class and explicit color
<CollapseIcon className="text-muted" color="var(--color-muted)" size={24} />

// Logo mark only
<LogoIcon size={32} />

// Logo + wordmark, scaled to 24px tall
<LogoWithFontIcon height={24} />
```

## Notes

- All layout icons (`CollapseIcon`, `ExpandIcon`) use a `20×20` viewBox.
- All functional icons (`ChatIcon`) use a `24×24` viewBox.
- The `fill` variant of `CollapseIcon` / `ExpandIcon` renders the directional arrow in
  `var(--panel, white)` so it contrasts against the filled panel. Override the CSS
  variable if the panel background differs from white.
- `IconProps` is defined once in `CollapseIcon.tsx` and re-imported by all other icon
  components to avoid duplication.
