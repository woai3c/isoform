# appparser

App-specific parsers that extract structured content from macOS accessibility (AX) trees. Used by the activity monitor to convert raw AX data from foreground applications into structured `ParsedContent` suitable for AI context.

## Overview

Each parser targets one or more macOS app bundle IDs and/or URL patterns and knows how to interpret that app's AX tree layout. Parsers register themselves via `init()` and are dispatched through a central registry.

## Key Components

| File | Purpose |
|------|---------|
| `appparser.go` | Core types: `ParsedContent`, `ConversationMessage`, `Parser`/`TextParser`/`TreeParser`/`URLMatcher` interfaces |
| `registry.go` | Global parser registry, `Register()`, `ParseTree()`, `Parse()` dispatch functions |
| `ax_tree.go` | AX tree utilities: `FindFirst`, `FindAll`, `Traverse`, `ChildrenByRole`, `HasClass`, `CollectStaticTexts` |
| `url_match.go` | `hostMatches()` helper for URL-based parser dispatch |
| `chatgpt.go` | ChatGPT native app (`com.openai.chat`) — extracts conversation via AXSplitGroup layout |
| `messages.go` | Apple Messages (`com.apple.MobileSMS`) — extracts iMessage conversations from AXGroup descriptions |
| `slack.go` | Slack (`com.tinyspeck.slackmacgap`) — extracts channel/DM conversations via AXDocument groups |
| `browser.go` | All major browsers (Chrome, Safari, Firefox, Arc, Edge, Brave, etc.) — strips browser chrome from AX text |
| `terminal.go` | Terminal emulators (Terminal, iTerm2, Ghostty, Warp, WezTerm, Kitty, Hyper) — trims to last 100 lines |
| `calendar.go` | Apple Calendar (`com.apple.iCal`) — extracts events with times and locations |
| `notes.go` | Apple Notes (`com.apple.Notes`) — extracts document body from AXWebArea or AXTextArea |
| `mail.go` | Apple Mail (`com.apple.mail`) — extracts email threads as conversations |
| `reminders.go` | Apple Reminders (`com.apple.reminders`) — extracts task lists with completion state |
| `github.go` | GitHub (`github.com`) — issues/PRs as conversations, diffs and pages as documents |
| `gmail.go` | Gmail (`mail.google.com`) — threads as conversations, list views as documents |
| `google_calendar.go` | Google Calendar (`calendar.google.com`) — extracts events using the same aria-label format as native Calendar |
| `notion.go` | Notion (`notion.so`, `*.notion.site`, `notion.id`) — extracts page content from web and native app |
| `obsidian.go` | Obsidian (`md.obsidian`) — extracts the active note's body from CodeMirror (edit mode) or `markdown-preview-view` (reading mode); vault name from window title |
| `finder.go` | Finder (`com.apple.finder`) — extracts current directory, file list, and **selected** filenames (List/Icon view); Column view degrades to status-bar summary because macOS 26 SwiftUI doesn't expose per-file AX nodes |
| `appstore.go` | App Store (`com.apple.AppStore`) — extracts product-page metadata (name, developer, rating, size, age, category, chart position, languages) via identifier-driven AX schema; tab landings produce a minimal "browsing App Store" signal |
| `chatgpt_web.go` | ChatGPT web (`chatgpt.com`, `chat.openai.com`) — extracts conversation messages via `user-message-bubble-color` / `agent-turn` DOM classes; complements the native `chatgpt.go` parser |
| `claude_web.go` | Claude.ai (`claude.ai`) — extracts conversation messages via `font-user-message` / `font-claude-response` DOM classes; strips " - Claude" suffix from the conversation title |
| `web_chat_common.go` | Shared `webChatSpec` + `webChatDriver` used by `chatgpt_web.go` and `claude_web.go`; turn-by-class detection, frame-y chronological sort, action-toolbar chrome filtering |
| `linear.go` | Linear (`linear.app`, `com.linear`) — issue/project details as documents, board views as task lists |

## Architecture

### Parser Interfaces

```go
// All parsers must implement CanParse (bundle-ID match, used for native apps).
type Parser interface {
    CanParse(bundleID string) bool
}

// URLMatcher: optional, for parsers that also handle a web version of the
// tool inside a browser (e.g. Notion at notion.so, Linear at linear.app).
// URL matches take priority over bundle-ID matches so tool-specific web
// parsers pre-empt the generic browser chrome stripper.
type URLMatcher interface {
    CanParseURL(url string) bool
}

// TextParser: operates on flat AX text strings (fallback path).
type TextParser interface {
    Parser
    Parse(axText, bundleID, windowTitle, url string) *ParsedContent
}

// TreeParser: operates on the structured AXNode tree (preferred path).
type TreeParser interface {
    Parser
    ParseTree(tree *AXNode, bundleID, windowTitle, url string) *ParsedContent
}
```

### Dispatch Priority

`registry.ParseTree()` tries parsers in this order:
1. **URL match** (when the capture includes a browser URL) — `TreeParser` first, then `TextParser`. Lets web-aware parsers pre-empt `browser.go`.
2. **Bundle-ID match** — `TreeParser` first, then `TextParser`. Native apps and the `browser.go` fallback live here.

### Auto-Registration

Every parser registers itself in its `init()` function:

```go
func init() {
    Register(&myParser{})
}
```

Importing the package is sufficient to activate all parsers.

## Content Types

`ParsedContent.ContentType` values:

| Value | Produced by |
|-------|------------|
| `"conversation"` | ChatGPT, Slack, Messages, Mail, GitHub (issues/PRs), Gmail (threads) |
| `"document"` | Notes, Notion, Linear (issue/project detail), GitHub (diffs, generic pages), Gmail (list views) |
| `"task_list"` | Reminders, Linear (board/cycle views) |
| `"events"` | Calendar, Google Calendar |
| `"web_page"` | Browser parsers (fallback) |
| `"terminal"` | Terminal parsers |
| `""` | Unparsed / no match |

## Usage

```go
import "wails-gui/internal/platform/activitymon/appparser"

// Preferred: structured tree + flat text fallback. Pass the active
// browser URL (empty string for native apps) so URL-aware parsers can
// dispatch.
result := appparser.ParseTree(axTree, flatText, bundleID, windowTitle, browserURL)

// Legacy: flat text only
result := appparser.Parse(flatText, bundleID, windowTitle, browserURL)

if result != nil {
    fmt.Println(result.ContentType, result.Channel, result.Body)
}
```

`ParseTree` returns `nil` when no parser matches or produces content — callers should fall back to raw AX text or OCR.

## AX Tree Utilities (`ax_tree.go`)

Helpers for walking `AXNode` trees:

```go
FindFirst(root, predicate)          // depth-first, returns first match
FindAll(root, predicate)            // depth-first, returns all matches (no descent into matched nodes)
Traverse(root, visit)               // depth-first, return false to prune subtree
ChildrenByRole(parent, role)        // direct children with matching Role
HasClass(n, className)              // exact DOM class match
HasClassContaining(n, substr)       // substring DOM class match (for CSS-in-JS)
IsSelected(n)                       // user-selection signal (kAXSelectedAttribute)
Text(n)                             // description > value > title precedence
CollectStaticTexts(root)            // all AXStaticText values in traversal order
```

The `Selected` field is populated by the C tree walker from
`kAXSelectedAttribute` and only emitted when true. It surfaces the
"user choice" signal (Finder file rows, NSTableView selections,
sidebar pickers, segmented controls) — the cheapest way to tell what
the user has clicked on inside a list-like surface.

`AXNode` and `AXFrame` are re-exported from `internal/platform/activitymon` so parser files don't need a separate import.

## Electron / Chromium AX Gating

Chromium-based desktop apps (Chrome, Slack, Discord, Linear, VS Code, …)
keep their WebView accessibility tree dormant until an external client
sets `AXEnhancedUserInterface = true` on the application AX element —
VoiceOver does this implicitly. Without that priming, the tree we walk
stops at the native `NSWindow` chrome (`RootView` → `NonClientView` →
`NativeFrameViewMac` → `ClientView` → `View`) and the entire web content
is invisible.

Priming is now done in `tree_walk_app_to_buf` (axtree_tree_darwin.go).
Bootstrap is asynchronous on Chromium's UI thread, so the helper sets the
attribute, sleeps ~30 ms, then walks. Verified empirically: Chrome jumps
from 8 → 500+ AX nodes after the call. Slack's main window goes from
~8 → ~400 nodes the second time the helper runs (the very first call
after process launch may still see the chrome-only tree because the
WebView accessibility manager hasn't initialised yet).

### Slack-specific gotcha: sr-only `AXContentList`

Slack publishes **two** `AXList[AXContentList]` nodes per channel:

1. A 1×1 `sr-only` marker that carries the channel name in `Description`
   but has **no children** — VoiceOver uses this to announce "you're in
   channel X".
2. The real virtual-scrolled message column, which is a plain `AXGroup`
   with class `c-virtual_list__scroll_container` (no `AXContentList`
   subrole at all).

The slack parser previously matched (1) and walked an empty subtree, so
it always returned 0 messages. The current implementation prefers a
`AXContentList` with non-trivial frame or non-empty children, and falls
back to scanning the entire window for `AXGroup[AXDocument]` wrappers
(class `c-message_kit__hover`) — those are the per-message containers
the original heuristic was already designed to consume.

If a chrome-only tree is observed (priming failed for some reason), the
slack parser returns a sentinel `ParsedContent` with
`Metadata["axGated"] = "true"` and emits a one-shot log so this is
greppable in production. The activity pipeline can use this signal to
record "user is in Slack but content is unreadable" rather than treating
the snapshot as a parser-miss indistinguishable from a generic web page.

### Diagnosing parser misses

Use the `cmd/ax-tree-dump` CLI to confirm what the parser actually sees
for a given app — it bypasses `NSWorkspace.frontmostApplication` (which
gets hijacked by `com.apple.universalcontrol` on multi-monitor setups
with iPad cursor crossover) by reading directly from the running app's
process by bundle ID:

```bash
go build -o /tmp/ax-tree-dump ./cmd/ax-tree-dump

# Inspect the parser's view of a target app's current focused window
/tmp/ax-tree-dump --bundle com.tinyspeck.slackmacgap --out /tmp/slack.json

# List all top-level windows of an app with per-window node counts
/tmp/ax-tree-dump --bundle com.tinyspeck.slackmacgap --list-windows

# Capture the frontmost app after a delay — useful when the target is
# not directly addressable by bundle ID
/tmp/ax-tree-dump --delay 5 --out /tmp/dump.json
```

The dump JSON includes the full AX tree, the parser's verdict
(`dispatch.matchedApp`, `dispatch.parsedNonEmpty`, `dispatch.messagesCount`),
and the resulting `ParsedContent` so you can see exactly which subtree
the parser zeroed in on.

## Pane Scoping Convention

Every parser MUST anchor to its app's active content container before
extracting text. Don't `Traverse(window, …)` then collect every
`AXStaticText` — that pulls in sidebar, nav rail, tab labels, preview
list items, and status bar text. The LLM consumer then receives a
parsed payload that mixes the focused content with everything else
visible on screen, and starts correlating unrelated material across
panes ("user was thinking about <sidebar item> while writing <main
pane>" when the two are independent contexts).

The C-side AX walker (`axtree_tree_darwin.go`) is intentionally
generic — it dumps the whole focused window. Pane scoping is the
parser's job, not the walker's. Each app exposes a different
"active content" anchor; the parser knows which one to use.

### Anchor patterns

The four shapes we've seen so far, with concrete examples:

| Pattern | Used by | Anchor |
|---------|---------|--------|
| Class-based (Electron / web) | Obsidian | `HasClass(n, "workspace-leaf") && HasClass(n, "mod-active")` |
| Class-based (Electron / web) | Notion | `HasClassContaining(n, "notion-page-content")` |
| Class-based (Electron / web) | VSCode / Cursor | `HasClassContaining(n, "monaco-editor")` |
| Class-based (Electron / web) | Feishu | `HasClass(n, "a11y_feed_card_item")` for chat list |
| Role + content shape | Notes, Terminal | Pick the longest `AXTextArea.Value` — sidebar is `AXOutline`, naturally excluded |
| Role + content shape | WhatsApp | Walk `AXStaticText`, match `.Description` against the message-shape regex; sidebar previews are contact names and don't match |
| Role + subrole | Slack | `AXGroup` with subrole `AXDocument` (the message-list virtualised scroll container) |
| Role + identifier / role + rich rows | Finder, Asana, Reminders | `AXTable` / `AXList` / `AXOutline` with the most non-empty `AXRow` children |

### How to verify a new parser is correctly scoped

Use the AX Parser DevTools panel (Settings → Dev Tools → AX Parser):

1. Open the target app with its sidebar / nav / preview list visible.
2. Set focus inside the main content area.
3. `Capture & Parse`.
4. Inspect the **AX-parsed body** field. It must contain ONLY the
   active content. If you see sidebar item names, navigation labels,
   preview snippets from inactive items, or tab titles, the parser is
   over-collecting and needs a tighter anchor.

The **Fused output (what the LLM sees)** field below shows the same
body prefixed with the `### [app] label` heading the activity
triager emits per frame — that's exactly the per-frame chunk the
triage LLM consumes.

### Empirical audit (2026-05-21)

Verified scoping against real multi-pane app states:

| App | Pane layout | Result | Anchor |
|-----|-------------|--------|--------|
| Notes | Folder rail + notes list + editor | ✓ clean | longest AXTextArea |
| Obsidian | File tree + calendar + multi-tab + editor | ✓ clean | `workspace-leaf.mod-active` |
| Notion | Sidebar (workspace tree, recents, agents, …) + page | ✓ clean | `notion-page-content` |
| Slack | Channel list + message pane | ✓ clean | `AXGroup[AXDocument]` |

PR #1798 left "in-window parsed bleed" as a documented follow-up
under the assumption that the walker would need to use
`kAXFocusedUIElement` to scope per-pane. The audit above shows the
parser-layer anchors already solve this for the apps where it would
matter. The follow-up does not require walker changes — only that new
parsers continue to follow the convention above.

## Adding a New Parser

1. Create a new file (e.g., `myapp.go`).
2. Define a struct and implement `CanParse` plus `TreeParser` or `TextParser`. Add `URLMatcher` if the app has a web version.
3. Anchor to the app's active content container (see [Pane Scoping Convention](#pane-scoping-convention)) — never `Traverse(window, …)` and harvest all static text.
4. Register in `init()`:

```go
type myAppParser struct{}

func init() { Register(&myAppParser{}) }

func (p *myAppParser) CanParse(bundleID string) bool {
    return bundleID == "com.example.myapp"
}

// Optional — also match the web version at myapp.com
func (p *myAppParser) CanParseURL(url string) bool {
    return hostMatches(url, "myapp.com")
}

func (p *myAppParser) ParseTree(tree *AXNode, bundleID, windowTitle, url string) *ParsedContent {
    // ...
    return &ParsedContent{ContentType: "document", Body: body}
}
```

## Dependencies

- `wails-gui/internal/platform/activitymon` — `AXNode` and `AXFrame` types
- Standard library only (`strings`, `regexp`, `sort`, `net/url`, `fmt`, `strconv`, `context`, `os/exec`, `log`, `sync/atomic`)

## Testing

Parser tests live alongside their implementations and construct fake `AXNode` trees to exercise parse logic without real accessibility data:

| Test file | Coverage |
|-----------|----------|
| `calendar_test.go` | Calendar event extraction, full-date filter, end-time recovery, sort key generation |
| `messages_test.go` | iMessage description parsing, date divider propagation, CSV field splitting |
| `notes_test.go` | AXTextArea body extraction, WebKit fallback path, title/body splitting |
| `notion_test.go` | SwiftUI textarea parsing, bullet-split block recovery, header chrome stripping |
| `obsidian_test.go` | Edit-mode cm-content extraction, reading-mode preview-view extraction, sidebar-plugin exclusion via `mod-active`, vault name parsing |
| `finder_test.go` | List view filename + selected extraction, Icon view AXGroup path, sidebar location vs file row disambiguation via `AXOutline.identifier`, Column view degraded path, browse-list cap |
| `appstore_test.go` | Third-party (Spark Mail layout) + first-party (Numbers layout, duplicate artwork badges) product page extraction, paragraph badge disambiguation (size vs languages), tab landing fallback |
| `web_chat_common_test.go` | ChatGPT-web + Claude-web spec dispatch, frame-y chronological ordering, prune-at-matched-turn (no double counting), action-toolbar chrome skip, host whitelist coverage, message-less page returns nil |
| `reminders_test.go` | Checklist rendering, parent-map back-walk, checkbox completion detection |
| `terminal_test.go` | AXTextArea body selection, iTerm2 AppleScript simulation, line trimming |

Run tests from the package directory:

```bash
go test ./wails-gui/internal/platform/activitymon/appparser/
```

Test patterns follow the same convention: build a minimal `AXNode` tree that mimics a real app's accessibility structure, invoke `ParseTree` directly on the parser struct, and assert on the returned `ParsedContent` fields (messages, body text, content type, metadata). Tests avoid importing `activitymon` directly — `AXNode` and `AXFrame` are re-exported from this package via type aliases, so test files are fully self-contained.

