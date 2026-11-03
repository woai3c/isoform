# Dialog

A centred 520px modal for a decision that needs the person's attention, on a blurred ink-at-40% scrim.

**Parts** `Dialog`, `DialogTrigger`, `DialogContent` (12px radius, `color-panel`, `color-border`, `shadow-card`, max 80vh, `rise-in` entrance), `DialogHeader` (20/24/16px padding, bottom hairline), `DialogTitle` (18px semibold `color-ink`), `DialogDescription` (14px `color-muted`), `DialogCloseButton`, `DialogClose`, `DialogOverlay`, `DialogPortal`.

**Consumer provides** a question-style title that names the consequence (`Turn off independent work?`), a description that says what happens and how to undo it, and a footer of Buttons (primary last).

**Note** the scrim is `color-ink` at 40%, so in One Dark it is a light wash — as in the source.

Source: `desktop/wails-gui/frontend/src/components/ui/dialog.tsx`. Static rendition.
