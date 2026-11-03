# contacts

React components for managing user contacts (principals) in the Yansu desktop app.

## Overview

This directory contains the contact management UI components used throughout the Wails GUI frontend. A "principal" represents a person the user interacts with — it can be the user themself (`kind: 'self'`), an explicitly created contact (`kind: 'contact'`), or an anonymous person auto-detected from message history (`kind: 'anonymous'`). These components provide dialogs and pickers for creating, editing, and selecting principals.

## Files

| File | Description |
|------|-------------|
| `contact-editor.tsx` | Dialog and subcomponents for creating, editing, and promoting contacts. Exports `ContactEditorDialog`, `HandleListEditor`, `ChipInput`, and `syncHandles`. |
| `contacts-import-dialog.tsx` | Consent and access management only; background sync is owned by Go. |
| `contact-link-dialog.tsx` | Search imported contacts, rank suggestions and link into a seen person. |
| `principal-picker.tsx` | Searchable list component for selecting a principal from all known people. Includes inline creation affordance. |

## Architecture

### ContactEditorDialog

A modal dialog that operates in three modes:

- **`create`** — Add a brand new contact with name, relationship, aliases, and app handles.
- **`edit`** — Modify an existing contact's name, relationship, and aliases; sync handles via diff.
- **`promote`** — Convert an anonymous principal into a full contact.

```tsx
import { ContactEditorDialog } from './contacts/contact-editor';

<ContactEditorDialog
  mode="create"
  open={open}
  onOpenChange={setOpen}
  onSaved={async (principal) => { /* ... */ }}
/>
```

### PrincipalPicker

A searchable, selectable list of all principals. Used for "who is this about?" flows such as re-attributing a misidentified memory.

```tsx
import { PrincipalPicker } from './contacts/principal-picker';

<PrincipalPicker
  selectedID={currentPrincipalId}
  onSelect={(principal) => setSubject(principal)}
  excludeKinds={['self']}
  allowCreate={true}
/>
```

### Reusable Subcomponents

Both exports below are used inside `ContactEditorDialog`:

- **`ChipInput`** — Tag-style input for entering/removing string lists (aliases, tags). Supports Enter/Comma to add, Backspace on empty to remove last.
- **`HandleListEditor`** — Inline editor for app handles (e.g. WeChat ID, Telegram handle). Shows existing handles and a compact form to add new ones.

### Handle Synchronization

`syncHandles(principalID, before, after)` diffs two handle arrays by `(app, handleId, displayHandle)` and issues the minimal `BindAppHandle`/`UnbindAppHandle` calls. Used in `edit` and `promote` modes to avoid wiping and re-creating handles.

## Data Model

Types are defined in `../../lib/wails-shim-app`:

- **`PrincipalDTO`** — A person entity with `id`, `kind`, `displayName`, `relationship`, `aliases`, `appHandles`, etc.
- **`AppHandleDTO`** — A bound messaging account: `app` (e.g. `wechat`), `handleId`, `displayHandle`.

Supported apps (must match chat renderers for resolver lookups):

```ts
export const SUPPORTED_APPS = [
  'wechat', 'telegram', 'slack', 'discord',
  'feishu', 'imessage', 'email',
];
```

Supported relationships:

```ts
export const RELATIONSHIPS = ['family', 'partner', 'friend', 'colleague', 'acquaintance'];
```

## i18n

All user-facing strings go through `react-i18next`:

- `contact-editor.tsx` uses the **`settings`** namespace.
- `principal-picker.tsx` uses the **`people`** namespace.

Keys must exist in both `en/` and `zh-CN/` locale files. See `src/i18n/README.md` for conventions.

## Dependencies

- `react` / `react-i18next` — UI and translations
- `sonner` — Toast notifications for save errors
- `lucide-react` — Icons (Loader2, Plus, X, Search, User, UserCircle, HelpCircle)
- `../../lib/wails-shim-app` — Wails Go bindings (CRUD + bind/unbind operations)
- `../ui/dialog` — Dialog shell components

## Usage in the App

| Consumer | Usage |
|----------|-------|
| `people-page.tsx` | `ContactEditorDialog` (create/edit/promote), `PrincipalPicker` (subject selection) |
| `memory-page.tsx` | `PrincipalPicker` (wrong-subject re-attribution) |
