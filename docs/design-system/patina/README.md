# Patina, the Isoform design tokens

Drop `tokens.css` in after (or instead of) `ds/yansu/tokens.css`: the names are the same, the values are Patina's, and texture and display type come with it. `tokens.json` is the same set for tooling. `icons/` holds the app icon sources (SVG) and the 1024 renders; the full macOS iconset is built from `app-icon-macos.svg`.

The direction, the meaning of each color and the order of work are on the Color page of the canvas (Color-Direction) and in patina-direction.md.

The app rollout reconciles the exported CSS with this JSON: seven light chip-text contrast corrections and the `color-copper`, `color-automation-bg` and `color-automation-border` additions are present in both. The JSON remains the tooling source; keep these two files in step.
