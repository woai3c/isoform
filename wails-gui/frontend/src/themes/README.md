# themes

Theme definitions for the Yansu Wails GUI application.

## Overview

This directory contains the theme system for the desktop app's UI. It defines terminal color palettes, theme metadata, and a lookup registry consumed by the `useTheme` hook and terminal components.

## Files

| File | Purpose |
|------|---------|
| `types.ts` | TypeScript types for all theme-related data structures |
| `violet.ts` | Resolves terminal canvas colors from the published brand token catalog |
| `registry.ts` | Central registry mapping theme IDs to definitions; exports defaults and a lookup helper |
| `paper.ts` | **Light** theme — brand violet on cool neutral gray (default light) |
| `onedark.ts` | **Dark** theme — brand violet on cool charcoal (default dark) |

## Types

### `ThemeId`
```ts
type ThemeId = 'paper' | 'onedark';
```

### `ThemePreference`
```ts
type ThemePreference = ThemeId | 'system';
```
`'system'` defers to the OS dark-mode setting, resolving to `paper` (light) or `onedark` (dark).

### `ThemeDefinition`
```ts
interface ThemeDefinition {
    id: ThemeId;
    label: string;      // Human-readable name shown in Settings
    isDark: boolean;
    terminal: TerminalTheme;
}
```

### `TerminalTheme`
Full 16-color xterm palette (`black`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `white` plus bright variants) plus `background`, `foreground`, `cursor`, and `selectionBackground`.

## Registry API

```ts
import { getTheme, themeList, themes, DEFAULT_LIGHT_THEME, DEFAULT_DARK_THEME } from '@/themes/registry';

getTheme('onedark');        // → ThemeDefinition (falls back to 'paper' on unknown id)
themeList;                  // → ThemeDefinition[]  (all themes as an array)
themes;                     // → Record<ThemeId, ThemeDefinition>
DEFAULT_LIGHT_THEME;        // → 'paper'
DEFAULT_DARK_THEME;         // → 'onedark'
```

## Available Themes

| ID | Label | Mode | Background |
|----|-------|------|-----------|
| `paper` | Light | Light | `color-background` (Patina paper) |
| `onedark` | Dark | Dark | `color-background` (Patina green-black) |

## Usage

Themes are surfaced via the `ThemeProvider` / `useTheme` hook in `src/hooks/useTheme.tsx`:

```tsx
import { useTheme } from '@/hooks/useTheme';

function MyComponent() {
    const { theme, terminalTheme, preference, setPreference } = useTheme();
    // theme.isDark, theme.terminal.background, etc.
}
```

The provider persists the user's preference to `localStorage` under the key `yansu.theme` and applies a `data-theme` attribute on `<html>` for CSS-based theming.

## Adding a New Theme

1. Create `src/themes/<name>.ts` exporting a `ThemeDefinition` object.
2. Add the new `ThemeId` to the union in `types.ts`.
3. Import and register the theme in `registry.ts`.
4. The Settings page picks it up automatically via `themeList`.
