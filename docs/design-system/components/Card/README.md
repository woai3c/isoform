# Card

A bordered hero panel (12px radius, `color-border`, `color-panel`, `shadow-card`) with a centred header and generous 40px padding — made for the landing/marketing surface, not the app UI.

**Parts** `Card`, `CardHeader` (flex column, 16px gap, 40px padding, centred), `CardTitle` (serif `card-title`: 32px semibold, wide tracking), `CardDescription` (14px `color-muted`), `CardContent` (40px x-padding, 48px bottom).

**Consumer provides** the title, description and content. For in-app grouped content use a plain `color-panel` / `color-panel-warm` container with a `color-border` hairline instead of `Card`.

Source: `desktop/wails-gui/frontend/src/components/ui/card.tsx`. Static rendition.
