# Logos

Copied verbatim from `LogoIcon.tsx` and `LogoWithFontIcon.tsx`, plus the macOS app icon from `build/appicon.png`.

- `yansu-mark.svg` is the logogram alone (19×19). Its ink is `color-ink` Paper, #202020. Use it on light grounds.
- `yansu-mark-light.svg` is the same mark in `color-ink` One Dark, #ede9de. Use it on dark grounds.
- `yansu-logo.svg` and `yansu-logo-light.svg` are the mark plus the YANSU wordmark (82×19, about 4.3:1). In code the width is always derived from the height.
- `app-icon.png` (1024²) is the white mark on a #5968df blue tile, used only as the app or dock icon. The blue isn't a UI token, so don't use it in the interface.

The mark is single-ink: never recolor its parts separately, add effects or redraw it. Its dot is the "eye", which may blink in-app.
