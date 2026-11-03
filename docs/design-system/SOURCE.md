# Where this design system comes from

The design system was made in Claude Design (claude.ai artifact "Yansu", type Design System, https://claude.ai/artifact/TYLB21c1ZsrZbSWB9dBkqh, version 1792606251-a188, last change 2026-10-21 18:10 UTC by Bo: "Contrast fixes: 15 color values adjusted so every text pair is 4.5:1+ and the focus border 3:1+ in both themes; Checkbox check now color-panel"). It was built from the app's own CSS and components in `wails-gui/frontend`.

## What is here

- `README.md`: the brand book (content, color, type, spacing, radii, elevation, motion, iconography, logo), as authored. It names the product Yansu; user-facing copy says Isoform (see CLAUDE.md), and code identifiers keep `yansu`.
- `tokens.json`: every token (color in Paper and One Dark, type, spacing, radius, shadow), the source of truth. `tokens.md` is a table generated from it.
- `components/<Comp>/README.md`: usage guidelines for the 18 documented components; `preview.html` beside each is a static rendition using `components/bundle.css`; `components/index.d.ts` documents their props.
- `api/components/<Comp>.md`: each component's card (props, parts, examples).
- `assets/Icons/README.md`, `assets/Logos/README.md`: how Isoform's own icons and logos are used. The files themselves stay in the app (`wails-gui/frontend/src/assets/icons/*.tsx`) and the artifact's asset store.

Not copied: the artifact's generated `tokens.css`, `api/tokens.md` and `manifest.json` (generated before the contrast fixes, so their values are stale; `tokens.json` and `tokens.md` are current), the artifact index `design-system.json`, and the artifact type's own viewer files. The app separately bundles PP Mori Book and Book Italic converted from the user's licensed OTFs, plus the artifact's SemiBold face, for display moments only; body UI type remains the system grotesque.

## Applied to the app

The app's CSS variables in `wails-gui/frontend/src/index.css` now equal `tokens.json` for every color token. The design system's contrast fixes changed 16 values:

| token | theme | before | now |
|---|---|---|---|
| `color-border-focus` | Paper | `#d6d4ce` | `#959081` |
| `color-border-focus` | One Dark | `#333638` | `#5f6569` |
| `color-subtle` | Paper | `#838383` | `#6f6f6f` |
| `color-label` | Paper | `#838383` | `#6f6f6f` |
| `color-faint` | Paper | `#bbbbbb` | `#737373` |
| `color-faint` | One Dark | `#6b6a66` | `#81807b` |
| `color-success` | Paper | `#15803d` | `#157d3c` |
| `color-error` | One Dark | `#ef4444` | `#f04d4d` |
| `color-extractor` | Paper | `#d4943a` | `#93641f` |
| `color-app-sage-text` | Paper | `#6c7a5c` | `#647155` |
| `color-app-teal-text` | Paper | `#5f7974` | `#5a726d` |
| `color-app-amber-text` | Paper | `#867259` | `#7c6952` |
| `color-app-rose-text` | Paper | `#85686a` | `#806466` |
| `color-app-indigo-text` | Paper | `#6d7b9a` | `#606d8b` |
| `color-app-plum-text` | Paper | `#796788` | `#776586` |
| `color-app-stone-text` | Paper | `#786f66` | `#736a62` |

`color-selection-code-fg` in One Dark stays `inherit` in CSS; the token file writes it as the alias `{color-code-fg}` because the format has no `inherit`, and the effect is the same.

## Keeping it in step

When a UI change adds or changes a token, change `index.css` and `tokens.json` together (and regenerate `tokens.md`), or update the design system artifact and copy its `project/` files here again.
