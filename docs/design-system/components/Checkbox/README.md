# Checkbox

A 16px Radix checkbox with a 4px radius; checked fills with `color-accent-2` and shows a 12px check.

**Consumer provides** `checked` / `onCheckedChange` and a visible label beside it (the component renders no label). Unchecked: `color-border-strong` edge on `color-background`. Focus: 1px `color-accent-2` ring. Disabled: 50% opacity.

**Check color** the check is `color-panel`, so it reads on `color-accent-2` in both themes (the source uses literal white, which nearly vanishes on One Dark's light #cfc9bd fill — change `text-white` to `text-panel` in checkbox.tsx to match).

Source: `desktop/wails-gui/frontend/src/components/ui/checkbox.tsx`. Static rendition (click to toggle).
