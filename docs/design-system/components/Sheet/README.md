# Sheet

A right-edge side panel (min(520px, 90vw)) for inspecting a record without leaving the page, sliding in over an ink-at-30% scrim.

**Parts** `Sheet`, `SheetTrigger`, `SheetContent`, `SheetHeader` (24/24/16px padding), `SheetTitle` (18px semibold, tight tracking), `SheetDescription` (12px `color-muted`), `SheetCloseButton` (28px, 8px radius), `SheetClose`.

**Consumer provides** the record's familiar name as the title, a one-line description (type · time), and the body. Exact IDs and enums go in a collapsible "Technical details" section, not the header.

Source: `desktop/wails-gui/frontend/src/components/ui/sheet.tsx`. Static rendition.
