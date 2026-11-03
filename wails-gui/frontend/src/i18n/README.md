# i18n

Wires [`react-i18next`](https://react.i18next.com/) into the Wails GUI frontend.

The active language is resolved at runtime via `i18next-browser-languagedetector`
(checks `localStorage` first, then the browser/system `navigator` locale) and falls
back to English when a key or language is missing.

## Layout

```
src/i18n/
  index.ts                  # i18next.init() — imported once from main.tsx
  i18next.d.ts              # TypeScript augmentation for typed t()
  locales/
    en/                     # English — source of truth for every key
      common.json
      app.json
      chat.json
      settings.json
      onboarding.json
      calendar.json
      activity.json
      knowledge.json
      memory.json
    zh-CN/                  # Simplified Chinese — mirrors en/ structure
      common.json
      app.json
      chat.json
      settings.json
      onboarding.json
      calendar.json
      activity.json
      knowledge.json
      memory.json
```

## Dependencies

| Package                              | Version | Purpose                              |
|--------------------------------------|---------|--------------------------------------|
| `i18next`                            | ^26.0.8 | Core i18n runtime                    |
| `react-i18next`                      | ^17.0.6 | React bindings (`useTranslation`)    |
| `i18next-browser-languagedetector`   | ^8.2.1  | Auto-detect user language        |

## Supported languages

| Code    | Language            |
|---------|---------------------|
| `en`    | English (default)   |
| `zh-CN` | Simplified Chinese  |

The active language key is persisted to `localStorage` under `i18nextLng`.

## Namespaces

| Namespace    | File               | Purpose                              |
|--------------|--------------------|--------------------------------------|
| `common`     | `common.json`      | Cross-cutting strings (Save, Cancel) |
| `app`        | `app.json`         | Top-level app shell / navigation     |
| `chat`       | `chat.json`        | Chat and conversation UI             |
| `settings`   | `settings.json`    | Settings pages                       |
| `onboarding` | `onboarding.json`  | Onboarding flow                      |
| `calendar`   | `calendar.json`    | Calendar view                        |
| `activity`   | `activity.json`    | Activity feed                        |
| `knowledge`  | `knowledge.json`   | Knowledge base                       |
| `memory`     | `memory.json`      | Memory panel                         |

## Conventions

1. **One namespace per feature.** Group related strings into a namespace named
   after the feature area. Reserve `common` for genuinely cross-cutting copy.
2. **English is the source of truth.** Every key must exist in `locales/en/<ns>.json`.
   Both release languages must contain every key in both directions.
   `pnpm test` runs `scripts/check-locales.mjs` before the frontend suite.
3. **No inline default values.** Always call `t('namespace:key')` — never
   `t('namespace:key', 'Save')`. Missing keys surface as bugs, not silent
   fallbacks.
4. **Flat keys are fine.** Light nesting (`welcome.heading`) is OK for grouping.
   Pick one style per namespace and stay consistent inside that file.
5. **Separators:** namespace separator is `:`, key separator is `.`.

## Do not translate

Some product and feature names stay in English in every locale, including
Simplified Chinese. They are proper names that match Isoform's docs and the
other tools Isoform builds on, so translating them would make the UI diverge
from the names users already see in those places. Keep the English spelling
inside an otherwise translated sentence (for example `Skill` inside a Chinese
sentence), rather than swapping in the Chinese word.

| Term            | Why it stays in English                                                        |
|-----------------|--------------------------------------------------------------------------------|
| Isoform         | The product name, unchanged in every language.                                 |
| Skill / Skills  | A particular feature name; the English word is the term of art, not a common noun. |
| MCP             | An industry protocol acronym (Model Context Protocol).                         |
| Claude Code     | The product name of an assistant Isoform builds on.                            |
| Codex           | The product name of an assistant Isoform builds on.                            |

The authoritative list lives in [`glossary.ts`](./glossary.ts) as the readonly
`UNTRANSLATED_TERMS` array; this README and the locale files point back to it.
`glossary.test.ts` fails any `zh-CN` value that translates one of these terms
away, so the list and the strings cannot drift apart.

### Adding a term

1. Add the name to `UNTRANSLATED_TERMS` in `glossary.ts`. Add the plural as its
   own entry when one exists (as with `Skill` / `Skills`) so the whole-word,
   case-insensitive check in `glossary.test.ts` catches both spellings.
2. List it in the table above with the reason it stays in English.
3. Run `pnpm test -- src/i18n` and fix every `zh-CN` string it flags by keeping
   the English term inside the Chinese sentence.

## Adding a new key

1. Open `locales/en/<feature>.json` (create the file if the namespace is new,
   then add it to `resources` and `ns` in `index.ts`).
2. Add the English string under a stable key:
   ```json
   { "newKey": "Hello world" }
   ```
3. Add the translation to each language file (e.g. `locales/zh-CN/<feature>.json`).
   Every key must exist in both release locales before shipping.
4. Use it in a component:
   ```tsx
   import { useTranslation } from 'react-i18next';

   function Greeting() {
       const { t } = useTranslation('chat');
       return <p>{t('newKey')}</p>;
   }
   ```
   Or with the namespace inline:
   ```tsx
   const { t } = useTranslation();
   t('chat:newKey');
   ```
5. The TypeScript augmentation in `i18next.d.ts` type-checks keys against the
   English JSON automatically — a typo becomes a compile error.

### Non-React usage

For code outside React components (helpers, utilities), import the `i18n` instance
directly and call `i18n.t()`:

```ts
import i18n from '@/i18n';

const message = i18n.t('common:cancel');
```

This is used in `src/lib/stream-helpers.ts` and other plain modules where hooks
are not available.

## Adding a new language

1. Create `locales/<lang-code>/` and copy every JSON file from `locales/en/`.
2. Translate the strings (untranslated keys will fall back to English).
3. Add `<lang-code>` to the `supportedLngs` array and the `resources` map in
   `index.ts`.

## Entry point

`main.tsx` imports i18n as a side-effect:

```tsx
import './i18n';
```

This runs `i18n.init()` exactly once at app startup. The `void` prefix on the init
call (`void i18n.use(...)`) suppresses the no-floating-promises lint rule without
blocking the initial render.

## Exports (`index.ts`)

| Export           | Type     | Description                                         |
|------------------|----------|-----------------------------------------------------|
| `default`        | `i18n`   | The initialized `i18next` instance                  |
| `defaultNS`      | `string` | `'common'` — default namespace for `t()` lookups    |
| `resources`      | `const`  | Map of language → namespace → JSON resource objects |
| `supportedLngs`  | `const`  | `['en', 'zh-CN']` — languages the detector may pick |

## TypeScript (`i18next.d.ts`)

The module augmentation constrains `t()` to keys defined in the English JSON files.
Adding a key to any `locales/en/<ns>.json` file automatically makes it available in
the typed API. An unknown key produces a compile error.

## Runtime notes

- **Suspense** is disabled (`react.useSuspense: false`) — no Suspense boundaries
  needed in components.
- **`returnNull` and `returnEmptyString`** are both `false` — an empty
  translation can never silently render as blank.
- **`interpolation.escapeValue`** is `false` — standard for React (XSS is
  handled by React's own escaping).
- **`debug`** is `false`; flip it locally in `index.ts` to trace lookups.
- **Language detection order:** `localStorage` → `navigator` (browser/system locale),
  cached to `localStorage` under the key `i18nextLng`.

## Testing

There are no dedicated i18n unit tests. The TypeScript augmentation in
`i18next.d.ts` acts as a compile-time safety net: a typo in a key name or
a missing English key produces a TypeScript error in any file that calls
`t()`. This catches the most common i18n bugs at build time.
