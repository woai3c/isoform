Isoform uses brand violet on cool neutral gray grounds. This direction was decided by Bo on 2026-10-23 and supersedes the older warm-paper and Patina guidance below. The authoritative reference is [the violet handoff](../violet/handoff.md), with [light](../violet/artboards/Violet-Today-Light.dc.html) and [dark](../violet/artboards/Violet-Today-Dark.dc.html) boards.

Violet means Isoform did, is doing or is saying something: automation, Isoform's lines, links, primary actions and selected tabs. Copper means waiting on you. Ink means you, headings and body text. Red and green remain failure and success. Grounds and buttons are flat; there is no Texture setting. Compatibility theme IDs remain `paper` (light) and `onedark` (dark). [tokens.json](tokens.json) and [tokens.md](tokens.md) mirror the supplied violet set. In-app marks use the theme accent; macOS and iOS app icons use the supplied violet mark on white.

Yansu is a local-first AI coworker. Its interface uses cool neutral grounds, violet actions and precise ink, and depth comes from hairlines, not glow. These rules apply to anything built for Yansu: app screens, the Desk, dialogs, settings, and marketing pages.

## Content fundamentals

Yansu speaks like a calm, capable coworker: direct, specific, transparent about evidence, clear about control, and quiet.

- Lead with the conclusion, current state or next action: `No matching result was found` comes before any mechanism.
- Use sentence case everywhere. Capitalize only Yansu, product names and known acronyms.
- Use nouns for destinations (`Detected tasks`, `History`, `About you`) and verb + object for commands (`Turn off independent work`, `Retry`). Never `Submit`, never `Activate boundary`.
- Name whose work it is when two actors appear: `Yansu's attempt` and `Your result`, not two items called `Run`.
- Never claim motive or permission the evidence doesn't support. Write `Yansu observed…` or `Yansu is testing…`, not `You prefer…`. `Ready`, `reliable` and `learned` never imply Yansu may act.
- Describe a state plus its consequence or recovery: `Independent work is paused. Update the selected priorities to resume.`
- Give metrics both a value and a unit: `0 results · 0 comparisons · 68% confidence`, not `0/0 · 68%`.
- Keep IDs, hashes, enum values, prompt versions and model names in a collapsible `Technical details` layer (see `Collapsible`). Show the translated label first (`Task type: Writing`) and keep the code (`compose.text`) exact inside.
- Empty states say what is absent in plain words; add an action only when one exists.
- No congratulation, urgency, personality filler, exclamation marks or emoji.

## Color

Two themes: **Paper** (light, the default) and **One Dark** (graphite). In code, `data-theme="onedark"` on the root switches theme; `system` follows the OS.

- Grounds climb a cool neutral ladder: page on `color-background`; raised surfaces (cards, menus, dialogs, inputs) on `color-panel`; grouped and recessed content on `color-panel-warm`. Hover is `color-surface-hover` and pressed or selected is `color-surface-active`. Use the shared hover tokens for consistency.
- Text: `color-ink` for primary text, `color-muted` for secondary (4.5:1+ on `color-background` and `color-panel` in both themes), `color-subtle` / `color-label` for tertiary metadata, and `color-faint` for the quietest text (transcript timestamps). Use ink and muted for essential small text; subtle and faint are restricted to metadata and decoration.
- The primary action is violet: `color-solid-btn-bg` with `color-solid-btn-fg`, with the paired foreground in each theme. `color-accent` and `color-accent-2` are the violet text accent. Use them for emphasis, links, focus and checked states.
- Hairlines: `color-border` by default, `color-border-strong` for menus and popovers, and `color-border-focus` for focused fields. Focus uses the supplied violet border token.
- Status: `color-error`, `color-success`, `color-warning` and `color-info`, each with a `-bg` fill and a `-border`. Always pair status color with a word or icon. Use the supplied status tokens with their paired fills; success icons can carry non-text contrast.
- Purpose colors stay in their lane: `color-seaweed-*` only for redaction and privacy animations, `color-automation-*` only for the automation indicator, `color-extractor` only for Timeline memory items, and `color-app-*-{bg,border,text}` only for per-app tint chips (chip text is 4.5:1+ on its fill).
- Code blocks are dark in both themes: `color-code-fg` on `color-code-bg`. Inline code sits on `color-panel-code`. Text selection is the sky wash `color-selection-bg`.

## Type

- Body UI type is the native grotesque: `font-sans` resolves to SF Pro on macOS and falls back to Inter Variable elsewhere. Display moments use the optional bundled PP Mori face with the system stack as fallback; portrait copy uses the same display family. Mono is SF Mono (`font-mono`). Serif (`font-serif`, Palatino) appears only in the landing `Card` title.
- The desktop bundles PP Mori Book and Book Italic as CSS weight 400, plus SemiBold at 600, in `wails-gui/frontend/src/assets/fonts/`. Book is used for the Today title, Activity headline, welcome heading and ready About you portrait. The Book faces were converted losslessly from the user's licensed OTFs; their internal weight metadata is 300, while the display design assigns them CSS weight 400. All three faces use `font-display: optional`.
- Scale: `display` 30 (1.25 line height), `portrait` 16 (1.66 line height, portrait v1.0.0), `heading-1` 22, `heading-2` 18, `heading-3` 16, `body` 14, `body-sm` 13, `caption` 12, `label` 11, `micro` 10. Dense app UI lives at 12.5–14px. Menus use 12.5px and 13px, and buttons use 14px semibold.
- Small group labels are uppercase with wide tracking in `color-muted` (11–12px, medium or semibold). Everything else is sentence case.
- Text renders antialiased (grayscale smoothing) app-wide.

## Spacing, radii, elevation

- Spacing follows Tailwind's 4px unit (`spacing`). Typical steps: 6–8px inside menu rows, 12px menu x-padding, 24px dialog and sheet padding, 40px for hero cards.
- Radii: `radius-md` (6px) for menu items and day cells; `radius-lg` (8px) for small and icon buttons, select triggers and context menus; `radius-xl` (12px) for cards, dialogs, popovers, menus, textareas and the command palette; `radius-full` for default buttons (pills), switches and avatars; `radius-sm` (4px) for checkboxes.
- Elevation is hairline-first. A 1px border plus `shadow-card` (a near-contact shadow and a tight drop) for floating panels, and `shadow-soft` for small raised items. No colored or glow shadows: `shadow-button` and `shadow-accent` are `none`. The primary button gets only the 1px inner highlight `shadow-solid-btn` in Paper.

## Motion

Conservative and quick. Hover and state swaps take 120ms, entrances 180ms, and larger surfaces 260ms. Entrances use ease-out `cubic-bezier(0.16, 1, 0.3, 1)`; reversible open/close uses ease-in-out `cubic-bezier(0.65, 0, 0.35, 1)`. Keep the spring curve `cubic-bezier(0.34, 1.56, 0.64, 1)` for small chips and badges. Dialogs `rise-in` (4px up, 96%→100% scale, 180ms), and overlays fade in over 120ms. Every animation has a `prefers-reduced-motion` fallback. These values live in the app CSS (`--motion-*`, `--animate-*`) and are not tokens here.

## Iconography

- Functional icons are lucide-react, used at 14–16px with `currentColor` and default stroke.
- Yansu's own icons (`assets/Icons`) are hand-drawn SVGs on 20 or 24px grids with round caps and joins, at 1.25–1.75 stroke. Use them for the sidebar and Recent list (Automation, Handoff, Meeting, Transcript, Today, Deliverable, Chat, Search, Collapse/Expand). In code they recolor via `currentColor`. The files here are fixed at `color-ink` Paper ink.
- No emoji in the UI.

## Logo

The Yansu mark is a calligraphic glyph with a dot "eye" (`assets/Logos/yansu-mark.svg`), and the full lockup adds the YANSU wordmark (`yansu-logo.svg`). Both are single-ink. Inside the app, use the theme accent. In-app, the mark's eye can blink, and only the eye animates. The macOS and iOS app icons use the supplied violet mark on white (`docs/violet/ds/icons`).

## Not synced

Sources are `desktop/wails-gui` from a local source snapshot (branch `isoexo/observe-memory-act`, commit `e52061c`). The GitHub repo itself was not reachable. Not brought in:

- the `--motion-*` and `--animate-*` variables, which have no token family here
- the Inter Variable font file, which comes from the npm package `@fontsource-variable/inter` and isn't committed
- `isoform.avif`, a 64px copy of the app-icon art in a format the asset store does not take

The 18 components are documented with static renditions (`components/bundle.css`), not a built React bundle.


---

## Consuming this system (generated — do not edit)

Every path named below is under `project/` in this design system: read `project/api/tokens.md`, not `api/tokens.md`.

18 components are documented without a runnable `components/bundle.js`: read each component’s card, and its README where it has one (`components/<Comp>/README.md`), and build to those guidelines. Tokens: the values are on `api/tokens.md`; a Slides deck or Design canvas also takes `tokens.json` by file path. On a Design canvas, also copy `tokens.css` to `project/ds/<folder>/tokens.css` in the call that installs this system, and link it in each artboard that uses this system, right after the `support.js` line and before the system’s other stylesheets: `<link rel="stylesheet" href="ds/<folder>/tokens.css">` (with one `../` in front for each folder the artboard is in).

Fonts: one file per family is enough (below: the upright face nearest regular weight; bold and italic synthesize). Fetch it as the fetch column says (Artifact tool `read`, that id or path as `path`), upload it as an asset (`publish`, `file_path`, `asset:true`) and declare it with an `@font-face { font-family: "<family>"; src: url(<uploaded>) }` rule.

- **Design canvas:** bring a font that is kept by id, or was not copied, as for any other page, with its `@font-face` in the artboard after the `tokens.css` link; the linked `tokens.css` cannot load it.
- **Slides deck:** for each family you use in the deck (`faces` holds at most the number of typefaces Slides’ `deck-files.md` gives), install its file with the system and name the path it lands on as that family’s `src` in `project/deck.json` `faces`, under the key in the Slides column. A deck takes a font as a file only under the rule in Slides’ `fonts.md` (no space or leading underscore in its path, for two): any other font, or one kept by id, is uploaded as `fonts.md` says.

| family | file | fetch | CSS | Slides `faces` key |
| --- | --- | --- | --- | --- |
| PPMori Medium Italic | `fonts/PPMori-MediumItalic.woff2` (weight 500, italic) | `read` the file with `project/` in front |  | `ppmori-medium-italic` |
| PPMori | `fonts/PPMori-Regular.woff` (weight 400) | `read c2e6a5f9521578def251d0f170b6c932` |  | `ppmori` |
| PPMori Regular Italic | `fonts/PPMori-RegularItalic.woff2` (weight 400, italic) | `read 173b598ad67576fe44533087557d4b81` |  | `ppmori-regular-italic` |
| PPMori Light Italic | `fonts/PPMori-LightItalic.woff2` (weight 300, italic) | `read 6a30727bcdda2f3f0b4eabbe6b866abc` |  | `ppmori-light-italic` |
| PPMori Extra Bold Italic | `fonts/PPMori-ExtraBoldItalic.woff` (weight 800, italic) | `read 11a00a8b7e554679e7250a91fc8f583a` |  | `ppmori-extra-bold-italic` |
| PPMori Book Italic | `fonts/PPMori-BookItalic.woff2` (weight 400, italic) | `read 1bea7af4333bb7dc47ea3c338b22a4b0` |  | `ppmori-book-italic` |
| PPMori Semi Bold Italic | `fonts/PPMori-SemiBoldItalic.woff` (weight 600, italic) | `read 53f8cda73cf0dfa2bc3e9981cfdeb1b8` |  | `ppmori-semi-bold-italic` |
| PPMori Bold Italic | `fonts/PPMori-BoldItalic.woff` (weight 700, italic) | `read c1982ae0974fd74b00a09ed1f3ae3a53` |  | `ppmori-bold-italic` |
| PPMori Extra Light Italic | `fonts/PPMori-ExtraLightItalic.woff2` (weight 200, italic) | `read 59d09eb3d542a583ff2c8a3d7a4f0a4a` |  | `ppmori-extra-light-italic` |

**Read, per thing:** a component’s props, parts and examples: `api/components/<Comp>.md`; token values: `api/tokens.md`; stored assets and their paths: `api/assets/<Group>.md`. After this README, fetch the cards and fonts you need in ONE message as parallel calls — none depends on another.

**Two rules.** Before you use a thing — a component, a token group, an icon, an asset — read its card from the index below; a value you did not read from a card is a guess. `tokens.json`, `manifest.json`, `components/index.d.ts` and `design-system.json` are sources for tools: hand them over. `components/<Comp>/README.md` and `assets/<Group>/README.md` are the long-form second read a card links to; `SKILL.md` and `artifact-type/` beside them are authoring guidance, not needed to consume the system.

## Index (generated — do not edit)

**Tokens**

- `api/tokens.md` — Every token: surface, text, fill, border, palette, type, spacing, radius, shadow. (26.0k)

**Icons and assets**

- `api/assets/Logos.md` — 5 files, by asset id. (1.8k)
- `api/assets/Icons.md` — 11 files, by asset id. (2.2k)

**Components** (`api/components/<Comp>.md`, 18)

- **Data display**: `Avatar` — A circular avatar showing an image, falling back to up to two uppercase initials on color-surface-active
- **Actions**: `Button` — The action control, with seven variants and four sizes; default and large are pills, small and icon are 8px-radius squares
- **Forms**: `Calendar` — A month-view date picker on react-day-picker supporting single, range and multiple selection · `Checkbox` — A 16px Radix checkbox with a 4px radius; checked fills with color-accent-2 and shows a 12px check · `Combobox` — A searchable select built on Popover: a search row, a filtered list with a check on the selected value, keyboard navigation, and virtualised rendering past 50… · `Select` — A Radix select: a 36px trigger (8px radius, color-border, 13px) and a content list with checkmark indicators and scroll buttons · `Switch` — A 36×20 Radix toggle for settings that take effect immediately; checked track is color-ink, unchecked is ink at 20% · `Textarea` — A resizable multi-line field: 72px min height, 12px radius, color-border on color-panel, 12×14px padding, 14px text
- **Layout**: `Card` — A bordered hero panel (12px radius, color-border, color-panel, shadow-card) with a centred header and generous 40px padding — made for the landing/marketing su… · `CollapsePanel` — An animated expand/collapse wrapper using the grid-template-rows 0fr↔1fr idiom, so content animates to its natural height and stays mounted (state preserved) w… · `Collapsible` — A thin re-export of Radix Collapsible for show/hide sections, such as the "Technical details" layer that holds IDs, enums and raw values · `ScrollArea` — A scroll container with a slim custom scrollbar (10px track, ink-at-10% rounded thumb) instead of the OS bar
- **Overlays**: `Command` — The command palette: a 520px cmdk list 20% from the top over a blurred ink-at-40% scrim, for keyboard-driven search and navigation · `Dialog` — A centred 520px modal for a decision that needs the person's attention, on a blurred ink-at-40% scrim · `Popover` — A floating panel (12px radius, color-border-strong, 16px padding) anchored to a trigger, for short explanations or small forms · `Sheet` — A right-edge side panel (min(520px, 90vw)) for inspecting a record without leaving the page, sliding in over an ink-at-30% scrim
- **Menus**: `ContextMenu` — A right-click menu (min 160px, 8px radius, color-border, shadow-card) for row-level actions · `DropdownMenu` — An action menu (min 12rem, 12px radius, color-border-strong, 4px padding) with optional labels, separators, sub-menus and destructive items

## Patina (decided 2026-10-22)

Bo decided the visual direction, Patina (docs/color/patina-direction.md). `patina/tokens.css` carries every token name from `tokens.json` with the Patina values, the texture variables and the display type; `patina/icons/` holds the mark and app icon sources. The app's `index.css` takes the Patina values (same names) once PR "Patina 1" lands; until then the values in this folder's `tokens.json` are the old Yansu set. Color carries meaning and each meaning has one color: verdigris (Isoform acted, is acting or speaking), copper (waiting on you), ink (you), red and green (failure and success); nothing colored for decoration. Grain on the ground and sidebar only; mottle on the primary button and the play and send circles only; Settings > Appearance has a Texture switch, on by default. PP Mori Book 400 for display moments only (30 px display, 16 px portrait); body stays the system font. "Main chat" is "Chat".
