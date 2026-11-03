# Button

The action control, with eight variants and four sizes; default and large are pills, small and icon are 8px-radius squares.

**Use** `default` (solid warm ink, `color-solid-btn-bg` / `color-solid-btn-fg`) for the one primary action on a surface: Continue, Get started, Turn off. `secondary` (warm outline, `color-sample-text` on `color-background`) for brand-adjacent secondary actions; `connect` for integration Connect buttons; `outline` (muted text, `color-border`) for Cancel and low-weight actions; `ghost` for pill toggles; `destructive` (text `color-error`, `color-error-bg` on hover) for actions that remove or stop work; `link` for inline text actions; `quiet` (text `color-muted`, no border, `color-surface-hover` with `color-ink` text on hover) for the lowest-weight action beside other Buttons, such as "Talk about this brief" after the brief's answers, "Forget" after Keep and Done, or "Set up in the main chat" after Open Settings (RC 1, added 2026-10-21).

**Consumer provides** the label as children (verb plus object, sentence case: `Turn on independent work`, never `Submit`), an `onClick`, and for `size="icon"` an `aria-label` and a 14–16px icon.

**Props** `variant`: default · secondary · connect · ghost · outline · destructive · link · quiet. `size`: default (40px, 18px x-padding) · sm (32px, 12px text) · lg (44px, 32px x-padding) · icon (32×32). `asChild` renders the styles onto a child element via Radix Slot. Disabled = 50% opacity, no pointer events. Focus ring: 2px `color-accent-2` at 40% with a 2px `color-background` offset.

**Don't** add shadows or colour: `shadow-button` is `none` and the primary action is a flat solid with only the 1px `shadow-solid-btn` highlight.

Source: `desktop/wails-gui/frontend/src/components/ui/button.tsx`. The preview is a static rendition styled by `components/bundle.css`.
