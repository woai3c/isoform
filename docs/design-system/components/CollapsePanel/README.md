# CollapsePanel

An animated expand/collapse wrapper using the grid-template-rows 0fr↔1fr idiom, so content animates to its natural height and stays mounted (state preserved) while closed.

**Props** `open` (boolean), `children`, `innerClassName`. Timing: 260ms (`--motion-duration-slow`) on `cubic-bezier(0.65, 0, 0.35, 1)`, fading opacity with height. Used by the processing block and composer suggestions.

**Consumer provides** the trigger and the `open` state; the panel itself renders no header.

Source: `desktop/wails-gui/frontend/src/components/ui/collapse-panel.tsx`. Static rendition (click to toggle).
