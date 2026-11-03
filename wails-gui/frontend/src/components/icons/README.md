# icons

SVG icon components for the Wails GUI frontend. Icons are inline SVG React components using theme role tokens.

## Files

### `app-icons.tsx`

Brand icons for macOS applications and communication platforms, sourced from [Simple Icons](https://simpleicons.org). Used to display app logos in the session/worktree launcher UI and notification integrations.

**App launcher exports** (accessed via `AppIcon`/`hasAppIcon`):

| Bundle Name | Icon | Color |
|---|---|---|
| `Visual Studio Code.app` | VSCode | `var(--color-ink)` |
| `Cursor.app` | Cursor | `var(--color-ink)` |
| `Windsurf.app` | Windsurf | `var(--color-ink)` |
| `Zed.app` | Zed | `var(--color-ink)` |
| `Sublime Text.app` | Sublime Text | `var(--color-ink)` |
| `Ghostty.app` | Ghostty | `var(--color-ink)` |
| `Warp.app` | Warp | `var(--color-ink)` |
| `iTerm.app` | iTerm2 | `var(--color-ink)` |
| `Alacritty.app` | Alacritty | `var(--color-ink)` |

**Named exports:**
- `AppIcon({ appBundle, className })` — renders the icon for a given macOS app bundle name, or `null` if not registered
- `hasAppIcon(appBundle)` — returns `true` if a brand icon exists for the given bundle
- `CursorIcon({ className })` — Cursor editor icon (`var(--color-ink)`); also registered in `appIconMap` for `AppIcon` lookup
- `YansuIcon({ className })` — the Isoform mark; renders the shared `LogoIcon` from `@/assets/icons/LogoIcon` with `yansu-logo text-accent` classes and `fill="var(--color-accent)"`
- `SlackIcon({ className })` — Slack logo (`var(--color-ink)`)
- `MicrosoftTeamsIcon({ className })` — Microsoft Teams logo (`var(--color-ink)`)
- `FeishuIcon({ className })` — Feishu/Lark logo (`var(--color-ink)`)
- `TelegramIcon({ className })` — Telegram logo (`var(--color-ink)`)
- `DiscordIcon({ className })` — Discord logo (`var(--color-ink)`)
- `WhatsAppIcon({ className })` — WhatsApp logo (`var(--color-ink)`)
- `WeChatIcon({ className })` — WeChat logo (`var(--color-ink)`)
- `ClaudeIcon({ className })` — Claude brand SVG logo (`var(--color-ink)`); distinct from the same-named export in `executor-icons.tsx` which renders the shared Isoform mark
- `ChatGPTIcon({ className })` — ChatGPT/OpenAI logo (`var(--color-ink)`)
- `CodexIcon({ className })` — OpenAI logo mark for Codex (`currentColor`); distinct from the same-named export in `executor-icons.tsx` which applies default sizing

### `sidebar-3d-icons.tsx`

3D faceted SVG icons for the sidebar navigation. Uses `React.useId()` to generate unique gradient IDs, ensuring correct rendering when multiple instances appear on the same page.

**Exports:**
- `ChatIcon3D({ className })` — speech-bubble icon for the Chat sidebar section; uses CSS custom property `--color-accent` for theme-responsive coloring
- `GroupIcon3D({ className })` — 3D folder icon for individual custom sidebar groups; per-face gradients from the panel, muted and icon-subtle tokens
- `GroupParentIcon3D({ className })` — 3D open-folder icon for the "Custom Groups" parent section header; distinguishes from `GroupIcon3D`
- `AutomationsIcon3D({ className })` — 3D clock icon for the Automations sidebar section; panel, muted and icon-subtle role tokens

### `executor-icons.tsx`

Icons for AI executor/model providers. Uses `cn()` from `@/lib/utils` for class merging.

**Exports:**
- `ClaudeIcon({ className })` — Isoform accent-colored `LogoIcon` (default size `h-3 w-3`) with its translated accessible name; represents the assistant in chat UI
- `CodexIcon({ className })` — OpenAI logo mark used for Codex (default size `h-3 w-3`, `currentColor`)

## Usage

```tsx
import { AppIcon, hasAppIcon, YansuIcon, SlackIcon } from '@/components/icons/app-icons';
import { ClaudeIcon, CodexIcon } from '@/components/icons/executor-icons';
import { ChatIcon3D, AutomationsIcon3D } from '@/components/icons/sidebar-3d-icons';

// Render an app icon by bundle name (returns null if not registered)
<AppIcon appBundle="Cursor.app" className="h-5 w-5" />

// Guard before rendering
{hasAppIcon(app.bundle) && <AppIcon appBundle={app.bundle} className="h-4 w-4" />}

// Communication platform icon
<SlackIcon className="h-5 w-5" />

// AI executor icons
<ClaudeIcon className="h-4 w-4" />
<CodexIcon />

// Sidebar 3D icons
<ChatIcon3D className="h-5 w-5" />
<AutomationsIcon3D className="h-5 w-5" />
```

All icons accept an optional `className` prop for sizing and styling via Tailwind CSS.

## Dependencies

- `@/assets/icons/LogoIcon` — SVG component used by `YansuIcon` in `app-icons.tsx`
- `@/assets/icons/LogoIcon` — accent-colored mark used by the executor icon; its accessible name uses `common:brand.name` in both locales
- `@/lib/utils` (`cn`) — used by `executor-icons.tsx` for Tailwind class merging
